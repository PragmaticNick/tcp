package iface

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"tcp-client/tcp"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
)

const sendQueueSize = 1024

type quad struct {
	srcIP   string
	srcPort uint16
	dst     string
	dstPort uint16
}

type Pending struct {
	quads []quad
	cond  *sync.Cond
}

type Connection struct {
	raw *tcp.Connection

	readChan chan struct{}
}

type connectionManager struct {
	// Active TCP connections keyed by quad (srcIP:srcPort, dstIP:dstPort)
	// This is used to handle incoming packets and manage connections
	connections map[quad]*Connection
	// Pending - binded ports with pending connections (quads) that can be accepted
	pending map[uint16]*Pending

	mu sync.Mutex
}

type Interface struct {
	ctx     context.Context
	manager *connectionManager
	nic     *water.Interface
}

func New(ctx context.Context, name, cidr string) (*Interface, error) {
	nic, err := setupTun(name, cidr)
	if err != nil {
		return nil, err
	}

	manager := &connectionManager{
		connections: make(map[quad]*Connection),
		pending:     make(map[uint16]*Pending),
	}

	iface := &Interface{
		ctx,
		manager,
		nic,
	}

	go iface.PacketLoop()

	return iface, nil

}

func (i *Interface) PacketLoop() {
	buffer := make([]byte, 1500)

	for {
		if i.ctx.Err() != nil {
			log.Println("Stopping packet loop due to context cancellation")
			return
		}

		n, err := i.nic.Read(buffer)
		if err != nil {
			log.Fatal(err)
		}

		packet := gopacket.NewPacket(buffer[:n], layers.LayerTypeIPv4, gopacket.Default)

		if iph, ok := packet.Layer(layers.LayerTypeIPv4).(*layers.IPv4); ok {
			if tcph, ok := packet.Layer(layers.LayerTypeTCP).(*layers.TCP); ok {

				quad := quad{
					srcIP:   iph.SrcIP.String(),
					srcPort: uint16(tcph.SrcPort),
					dst:     iph.DstIP.String(),
					dstPort: uint16(tcph.DstPort),
				}

				// If connection exist just handle the packet
				if _, exists := i.manager.connections[quad]; exists {
					conn := i.manager.connections[quad]
					err = conn.raw.OnPacket(i.nic, iph, tcph, tcph.Payload)
					if err != nil {
						fmt.Printf("Error handling packet: %v\n", err)
					}

					if len(conn.raw.Incoming) != 0 {
						conn.readChan <- struct{}{}
					}
				} else {
					// If someone listens on the destination port, than accept connection and add quad to pending
					// The pending quad can later be accepted by TcpListener.Accept()
					if pending, exists := i.manager.pending[quad.dstPort]; exists {
						pending.cond.L.Lock()
						conn, err := tcp.Accept(i.nic, iph, tcph, buffer[:n])
						if err != nil {
							fmt.Printf("Error accepting connection: %v\n", err)
							pending.cond.L.Unlock()
							continue
						}

						pending.quads = append(pending.quads, quad)
						i.manager.pending[quad.dstPort] = pending
						i.manager.connections[quad] = &Connection{
							raw:      conn,
							readChan: make(chan struct{}, 1),
						}

						pending.cond.Broadcast()
						pending.cond.L.Unlock()
					}
				}
			}
		}
	}
}

func (i *Interface) Bind(port uint16) (*TcpListener, error) {
	if _, exists := i.manager.pending[port]; exists {
		return nil, fmt.Errorf("port %d is already in use", port)
	}

	i.manager.pending[port] = &Pending{
		quads: make([]quad, 0),
		cond:  sync.NewCond(&sync.Mutex{}),
	}

	return &TcpListener{
		port:    port,
		manager: i.manager,
	}, nil
}

type TcpListener struct {
	port    uint16
	manager *connectionManager
}

func (l *TcpListener) Accept() (*TcpStream, error) {
	for {
		pending, ok := l.manager.pending[l.port]
		if !ok {
			return nil, fmt.Errorf("port %d is closed", l.port)
		}

		pending.cond.L.Lock()
		if len(pending.quads) > 0 {
			quad := pending.quads[0]
			pending.quads = pending.quads[1:]
			pending.cond.L.Unlock()

			return &TcpStream{
				quad:    &quad,
				manager: l.manager,
			}, nil
		}

		pending.cond.Wait()
		pending.cond.L.Unlock()
	}
}

type TcpStream struct {
	quad    *quad
	manager *connectionManager
}

func (s *TcpStream) Read(b []byte) (int, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()

	conn, ok := s.manager.connections[*s.quad]

	if !ok {
		return 0, fmt.Errorf("tcp stream terminated unexpectedly")
	}

	if len(conn.raw.Incoming) == 0 {
		fmt.Println("Locked on read, waiting for data...")
		<-conn.readChan
	}

	bytesToRead := min(len(b), len(conn.raw.Incoming))
	copy(b, conn.raw.Incoming[:bytesToRead])
	conn.raw.Incoming = conn.raw.Incoming[bytesToRead:]

	return bytesToRead, nil
}

func (s *TcpStream) Write(b []byte) (int, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()

	conn, ok := s.manager.connections[*s.quad]
	if !ok {
		return 0, fmt.Errorf("tcp stream terminated unexpectedly")
	}

	if len(conn.raw.Outgoing) >= sendQueueSize {
		// TODO: block
		return 0, fmt.Errorf("send queue is full")
	}

	bytesToWrite := min(len(b), sendQueueSize-len(conn.raw.Outgoing))
	conn.raw.Outgoing = append(conn.raw.Outgoing, b[:bytesToWrite]...)

	// TODO: Wake up writer

	return bytesToWrite, nil
}

func (s *TcpStream) Flush() error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()

	conn, ok := s.manager.connections[*s.quad]
	if !ok {
		return fmt.Errorf("tcp stream terminated unexpectedly")
	}

	if len(conn.raw.Outgoing) == 0 {
		return nil
	}

	// TODO: block
	return fmt.Errorf("flush not implemented, send queue is not empty")
}

func (s *TcpStream) Close() error {
	panic("not implemented")
}

func setupTun(name, cidr string) (*water.Interface, error) {
	config := water.Config{
		DeviceType: water.TUN,
	}
	config.Name = name
	iface, err := water.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN interface: %w", err)
	}

	link, err := netlink.LinkByName(config.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get link by name: %w", err)
	}

	ip, ipNet, err := net.ParseCIDR(cidr)
	ipNet.IP = ip
	if err != nil {
		return nil, fmt.Errorf("failed to parse CIDR: %w", err)
	}

	err = netlink.AddrAdd(link, &netlink.Addr{IPNet: ipNet})
	if err != nil {
		return nil, fmt.Errorf("failed to add address to link: %w", err)
	}

	err = netlink.LinkSetUp(link)
	if err != nil {
		return nil, fmt.Errorf("failed to set link up: %w", err)
	}

	return iface, nil

}
