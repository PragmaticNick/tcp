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

	counter uint32
}

func Accept(nic *water.Interface, iph *layers.IPv4, tcp *layers.TCP, _ []byte) (*Connection, error) {
	if !tcp.SYN {
		return nil, fmt.Errorf("accept called with non-SYN packet")
	}

	var iss uint32 = 0
	var wnd uint16 = 1024
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
			wnd: wnd,
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

func (c *Connection) OnPacket(nic *water.Interface, _ *layers.IPv4, tcp *layers.TCP, data []byte) error {
	fmt.Printf("\n\n\nPacket #%v\n", c.counter)
	c.counter++
	// print c.recv:
	fmt.Printf("Received packet: seq %d, ack %d, wnd %d\n", tcp.Seq, tcp.Ack, tcp.Window)
	fmt.Printf("conn.recv: nxt %d, wnd %d, irs %d\n", c.recv.nxt, c.recv.wnd, c.recv.irs)
	fmt.Printf("data[%v]: %s\n", len(data), string(data))

	acceptable := c.isSegmentAcceptable(tcp, data)
	if !acceptable {
		return fmt.Errorf("segment not acceptable: seq %d, nxt %d, wnd %d", tcp.Seq, c.recv.nxt, c.recv.wnd)
	}

	if !tcp.ACK {
		return fmt.Errorf("expected ACK in packet, got %v", tcp.ACK)
	}

	if c.state == SynReceived {
		if isBetweenWrapped(c.send.una-1, tcp.Ack, c.send.nxt+1) {
			c.state = Established
		} else {
			// TODO
		}
	}

	if c.state == Established {
		if isBetweenWrapped(c.send.una, tcp.Ack, c.send.nxt+1) {
			c.send.una = tcp.Ack
		}
		c.Incoming = append(c.Incoming, data[(c.recv.nxt-tcp.Seq):]...)

		c.recv.nxt = tcp.Seq + uint32(len(data))

		c.write(nic, nil)
	}

	return nil
}

func (c *Connection) isSegmentAcceptable(tcp *layers.TCP, data []byte) bool {
	segmentLength := uint32(len(data))
	windowEnd := c.recv.nxt + uint32(c.recv.wnd)

	if segmentLength == 0 {
		if c.recv.wnd == 0 {
			if tcp.Seq != c.recv.nxt {
				return false
			}
		} else {
			if !isBetweenWrapped(c.recv.nxt-1, tcp.Seq, windowEnd) {
				return false
			}
		}
	} else {
		if c.recv.wnd == 0 {
			return false
		} else {
			if !isBetweenWrapped(c.recv.nxt-1, tcp.Seq, windowEnd) &&
				!isBetweenWrapped(c.recv.nxt-1, tcp.Seq+segmentLength-1, windowEnd) {
				return false
			}
		}
	}

	return true
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

	fmt.Printf("Sent packet: seq %d, ack %d, wnd %d\n", c.tcp.Seq, c.tcp.Ack, c.send.wnd)

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
