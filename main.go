package main

import (
	"fmt"
	"log"
	"net"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
)

type Quad struct {
	srcIP   string
	srcPort uint16
	dst     string
	dstPort uint16
}

func main() {
	config := water.Config{
		DeviceType: water.TUN,
	}
	config.Name = "tun0"
	iface, err := water.New(config)
	if err != nil {
		log.Fatal(err)
	}

	link, err := netlink.LinkByName(config.Name)
	if err != nil {
		log.Fatal(err)
	}

	ip, ipNet, err := net.ParseCIDR("192.168.10.1/24")
	ipNet.IP = ip
	if err != nil {
		log.Fatal(err)
	}

	err = netlink.AddrAdd(link, &netlink.Addr{IPNet: ipNet})
	if err != nil {
		log.Fatal(err)
	}

	err = netlink.LinkSetUp(link)
	if err != nil {
		log.Fatal(err)
	}

	connections := make(map[Quad]*Connection)

	buffer := make([]byte, 1500)

	for {
		n, err := iface.Read(buffer)
		if err != nil {
			log.Fatal(err)
		}

		packet := gopacket.NewPacket(buffer[:n], layers.LayerTypeIPv4, gopacket.Default)

		if layer := packet.Layer(layers.LayerTypeIPv4); layer != nil {
			iph, _ := layer.(*layers.IPv4)

			if layer := packet.Layer(layers.LayerTypeTCP); layer != nil {
				tcph, _ := layer.(*layers.TCP)

				quad := Quad{
					srcIP:   iph.SrcIP.String(),
					srcPort: uint16(tcph.SrcPort),
					dst:     iph.DstIP.String(),
					dstPort: uint16(tcph.DstPort),
				}

				if _, exists := connections[quad]; !exists {
					conn, err := accept(iface, iph, tcph, buffer[:n])
					if err != nil {
						fmt.Printf("Error accepting connection: %v\n", err)
						continue
					}
					connections[quad] = conn
				} else {
					err = connections[quad].onPacket(iface, iph, tcph, buffer[:n])
					if err != nil {
						fmt.Printf("Error handling packet: %v\n", err)
						continue
					}
				}
			}
		}

	}
}
