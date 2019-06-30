package main

import (
	"fmt"
	"github.com/namsral/flag"
	"golang.org/x/net/dns/dnsmessage"
	"log"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Response struct {
	body []byte
	err  error
}

var dnsServers []string
var timeout *time.Duration

type Session struct {
	sock   *net.UDPConn
	client *net.UDPAddr
	data   []byte
}

type Pool struct {
	taskChan chan *Session
	cache    map[string]time.Time
	lock     sync.RWMutex
}

func (p *Pool) enqueue(s *Session) {
	p.taskChan <- s
}

func NewPool() *Pool {
	p := &Pool{taskChan: make(chan *Session, 2), cache: make(map[string]time.Time)}
	for i := 0; i < runtime.NumCPU(); i++ {
		go p.worker()
	}
	go p.deleteOldKeys()
	return p
}

func (p *Pool) worker() {
	for {
		select {
		case sess := <-p.taskChan:
			p.process(sess)
		}
	}
}

func (p *Pool) check(s string) (err error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if v, ok := p.cache[s]; ok {

		if time.Now().Sub(v) > time.Second {
			p.cache[s] = time.Now()
			return
		}
		err = fmt.Errorf("exists")
		return
	}
	p.cache[s] = time.Now()

	return
}
func (p *Pool) deleteKey(key string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.cache, key)
}

func (p *Pool) deleteOldKeys() {
	for {
		for k, v := range p.cache {
			if time.Now().Sub(v) > 5*time.Second {
				p.deleteKey(k)
			}
		}
		time.Sleep(10 * time.Second)
	}
}

func main() {
	servers := flag.String("dnsservers", "127.0.0.1:53", "comma separated list of dns servers")
	listen := flag.String("listen", ":5353", "listen on")
	timeout = flag.Duration("timeout", time.Second, "dns requests timeout")
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

	log.Printf("nxforwarder accepts dns requests on %s, forward to %s, timeout %s",
		*listen,
		strings.Join(dnsServers, ", "),
		*timeout,
	)
	buf := make([]byte, 1500)
	pool := NewPool()
	for {
		n, sourceAddr, err := sock.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		b := make([]byte, n)
		copy(b, buf[:n])
		pool.enqueue(&Session{data: b, sock: sock, client: sourceAddr})
	}

}
func (p *Pool) process(sess *Session) (err error) {

	var (
		res  Response
		bnx  []byte
		ok   bool
		err2 error
		m    dnsmessage.Message
	)
	if err = m.Unpack(sess.data); err != nil {
		return
	}
	if err = p.check(m.Header.GoString()); err != nil {
		//loop protection
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
				if err != nil {
					ch <- Response{nil, err}
				} else {
					ch <- Response{buffer[:n], err}
				}
				wg.Done()
			}()
			conn, err := net.DialTimeout("udp", s, *timeout)
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(*timeout))

			_, err = conn.Write(sess.data)
			if err != nil {
				return
			}

			n, err = conn.Read(buffer)
			if err != nil {
				return
			}

		}(s)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

loop:
	for {
		select {
		case res, ok = <-ch:
			if !ok {
				break loop
			}
			if res.err != nil {
				err2 = res.err
				continue
			}
			var m dnsmessage.Message
			if err := m.Unpack(res.body); err != nil {
				err2 = err
				continue
			}
			if m.RCode == dnsmessage.RCodeSuccess {
				sess.sock.WriteTo(res.body, sess.client)
				return
			} else {
				bnx = res.body
			}

		}
	}

	if bnx != nil {
		if _, err = sess.sock.WriteTo(bnx, sess.client); err != nil {
			return
		}
	}
	if err2 != nil {
		msg := dnsmessage.Message{Header: dnsmessage.Header{ID: id, Response: true, RCode: dnsmessage.RCodeServerFailure}}
		buf, err2 := msg.Pack()
		if err2 != nil {
			return
		}
		if _, err = sess.sock.WriteTo(buf, sess.client); err != nil {
			return
		}
		return
	}

	return
}
