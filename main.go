package main

import (
	"fmt"
	"github.com/namsral/flag"
	"golang.org/x/net/dns/dnsmessage"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

type Response struct {
	body []byte
	err  error
}

var dnsServers []string

func main() {
	servers := flag.String("dnsservers", "127.0.0.1:53", "comma separated list of dns servers")
	listen := flag.String("listen", ":5353", "listen on")
	flag.Parse()
	addr, err := net.ResolveUDPAddr("udp", *listen)
	if err != nil {
		log.Fatalln(err)
	}
	sock, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalln(err)
	}
	spl := strings.Split(*servers, ",")
	if len(spl) == 0 {
		log.Fatalln("empty list of servers")
	}
	dnsServers = make([]string, len(spl))
	for i, s := range spl {
		if !strings.Contains(s, ":") {
			dnsServers[i] = fmt.Sprintf("%s:53", s)
		} else {
			dnsServers[i] = s
		}
	}

	log.Printf("nxforwarder accepts dns requests on %s", *listen)
	buf := make([]byte, 1500)

	for {
		n, sourceAddr, err := sock.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		b := make([]byte, n)
		copy(b, buf[:n])
		go process(sourceAddr, b, sock)
	}

}
func process(udpaddr *net.UDPAddr, buf []byte, sock *net.UDPConn) (err error) {

	var m dnsmessage.Message
	if err = m.Unpack(buf); err != nil {
		return
	}
	id := m.ID
	var wg sync.WaitGroup
	wg.Add(len(dnsServers))
	ch := make(chan Response, len(dnsServers))
	for _, s := range dnsServers {
		go func(s string) {
			var n int
			var err error
			buffer := make([]byte, 1500)
			defer func() {
				wg.Done()
				if err != nil {
					ch <- Response{nil, err}
				} else {
					ch <- Response{buffer[:n], err}
				}
			}()
			conn, err := net.DialTimeout("udp", s, time.Second)
			if err != nil {
				return
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			conn.SetWriteDeadline(time.Now().Add(time.Second))
			_, err = conn.Write(buf)
			if err != nil {
				return
			}

			n, err = conn.Read(buffer)
			if err != nil {
				return
			}

		}(s)
	}

	wg.Wait()
	close(ch)

	var b Response
	var bnx []byte
	for b = range ch {
		if b.err != nil {
			continue
		}
		var m dnsmessage.Message
		if err := m.Unpack(b.body); err != nil {
			continue
		}
		if m.RCode == dnsmessage.RCodeSuccess {
			sock.WriteTo(b.body, udpaddr)
			return
		} else {
			bnx = b.body
		}
	}
	if bnx != nil {
		if _, err = sock.WriteTo(bnx, udpaddr); err != nil {
			return
		}
	}
	if b.err != nil {
		msg := dnsmessage.Message{Header: dnsmessage.Header{ID: id, Response: true}}
		buf, err2 := msg.Pack()
		if err2 != nil {
			return
		}
		if _, err = sock.WriteTo(buf, udpaddr); err != nil {
			return
		}
		return
	}

	return
}
