package tcp

import (
	"fmt"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/songgao/water"
)

const (
	Closed = iota
	Listen
	SynReceived
	Established
	FinWait1
	FinWait2
	Closing
)

type sendSequence struct {
	una uint32
	nxt uint32
	wnd uint16
	up  bool
	wl1 uint32
	wl2 uint32
	iss uint32
}

type recvSequence struct {
	nxt uint32
	wnd uint16
	up  bool
	irs uint32
}

type Connection struct {
	state int
	send  sendSequence
	recv  recvSequence

	ip  *layers.IPv4
	tcp *layers.TCP

	Incoming []byte
	Outgoing []byte
}

func (c *Connection) OnPacket(nic *water.Interface, _ *layers.IPv4, tcp *layers.TCP, data []byte) error {
	wend := c.recv.nxt + uint32(c.recv.wnd)
	seglen := uint32(len(data))

	if seglen == 0 {
		if c.recv.wnd == 0 {
			if tcp.Seq != c.recv.nxt {
				return fmt.Errorf("receive window is zero, cannot process data with seq %d", tcp.Seq)
			}
		} else {
			if !isBetweenWrapped(c.recv.nxt-1, tcp.Seq, wend) {
				return fmt.Errorf("seq %d is not in the receive window [%d, %d)", tcp.Seq, c.recv.nxt, wend)
			}
		}
	} else {
		if c.recv.wnd == 0 {
			return fmt.Errorf("receive window is zero, cannot process data")
		} else {
			if !isBetweenWrapped(c.recv.nxt-1, tcp.Seq, wend) &&
				!isBetweenWrapped(c.recv.nxt-1, tcp.Seq+uint32(len(data))-1, wend) {
				return fmt.Errorf("seq %d is not in the receive window [%d, %d)", tcp.Seq, c.recv.nxt, wend)
			}
		}
	}
	c.recv.nxt = tcp.Seq + seglen

	if !tcp.ACK {
		return fmt.Errorf("expected ACK in packet, got %v", tcp.ACK)
	}

	if isBetweenWrapped(c.send.una, tcp.Seq, c.send.nxt+1) {
		if !isStateSynchronized(c.state) {
			c.send.nxt = tcp.Ack
			c.write_rst(nic)
		}
		return fmt.Errorf("received packet with seq %d, but una %d and nxt %d", tcp.Seq, c.send.una, c.send.nxt)
	}
	c.send.una = tcp.Ack

	switch c.state {
	case SynReceived:
		if !tcp.ACK {
			return fmt.Errorf("expected ACK in SynReceived state, got %v", tcp.ACK)
		}
		c.state = Established
	case Established:
		fmt.Println("not implemented: handling data in Established state")

		fmt.Printf("data len: %v\n", len(data))
	}
	return nil
}

func Accept(nic *water.Interface, iph *layers.IPv4, tcp *layers.TCP, _ []byte) (*Connection, error) {
	if !tcp.SYN {
		return nil, fmt.Errorf("accept called with non-SYN packet")
	}

	var iss uint32 = 0
	c := &Connection{
		state: SynReceived,
		recv: recvSequence{
			irs: tcp.Seq,
			nxt: tcp.Seq + 1,
			wnd: tcp.Window,
			up:  false,
		},
		send: sendSequence{
			iss: iss,
			una: iss,
			nxt: iss + 1,
			wnd: 10,
			up:  false,
			wl1: 0,
			wl2: 0,
		},
	}

	c.tcp = &layers.TCP{
		SrcPort:    tcp.DstPort,
		DstPort:    tcp.SrcPort,
		Seq:        c.send.iss,
		Ack:        c.recv.nxt,
		Window:     c.send.wnd,
		SYN:        true,
		ACK:        true,
		DataOffset: 5,
	}
	c.ip = &layers.IPv4{
		Version:  4,
		IHL:      5,
		SrcIP:    iph.DstIP,
		DstIP:    iph.SrcIP,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
	}
	c.write(nic, nil)
	return c, nil
}

func (c *Connection) write(nic *water.Interface, data []byte) error {
	c.tcp.Seq = c.send.nxt
	c.tcp.Ack = c.recv.nxt
	c.tcp.SetNetworkLayerForChecksum(c.ip)

	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	buffer := gopacket.NewSerializeBuffer()
	gopacket.SerializeLayers(buffer, opts, c.ip, c.tcp)
	_, err := nic.Write(buffer.Bytes())
	if err != nil {
		return fmt.Errorf("failed to write packet: %w", err)
	}

	c.send.nxt += uint32(len(data))

	if c.tcp.SYN {
		c.send.nxt++
		c.tcp.SYN = false
	}

	if c.tcp.FIN {
		c.send.nxt++
		c.tcp.FIN = false
	}

	return nil
}

func (c *Connection) write_rst(nic *water.Interface) error {
	c.tcp.Seq = 0
	c.tcp.Ack = 0
	c.tcp.RST = true

	return c.write(nic, nil)
}
