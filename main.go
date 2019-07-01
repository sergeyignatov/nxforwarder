package main

import (
	"fmt"
	"github.com/namsral/flag"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/dns/dnsmessage"
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

		if time.Now().Sub(v) > 0 {
			p.cache[s] = time.Now().Add(time.Second)
			return
		}
		err = fmt.Errorf("exists")
		return
	}
	p.cache[s] = time.Now()

	go func(key string) {
		<-time.After(2 * time.Second)
		p.deleteKey(key)
	}(s)

	return
}
func (p *Pool) deleteKey(key string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.cache, key)
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
	log.SetFormatter(&log.TextFormatter{
		DisableColors: true,
		FullTimestamp: true,
	})
	log.Infof("nxforwarder accepts dns requests on %s, forward to %s, timeout %s",
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

func questions(m *dnsmessage.Message) string {
	t := make([]string, len(m.Questions))
	for i, v := range m.Questions {
		t[i] = v.GoString()
	}
	return strings.Join(t, ", ")
}

func (p *Pool) process(sess *Session) (err error) {

	var (
		res Response
		bnx []byte
		ok  bool
		m   dnsmessage.Message
	)
	if err = m.Unpack(sess.data); err != nil {
		return
	}
	key := fmt.Sprintf("%d - %s", m.ID, questions(&m))
	if err = p.check(key); err != nil {
		//loop protection
		log.Warnf("loop detected, %s", key)
		return
	}
	for _, x := range m.Questions {
		x.GoString()
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
				continue
			}
			var m dnsmessage.Message
			if err := m.Unpack(res.body); err != nil {
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

	msg := dnsmessage.Message{Header: dnsmessage.Header{ID: id, Response: true, RCode: dnsmessage.RCodeServerFailure}}
	buf, err := msg.Pack()
	if err != nil {
		return
	}
	if _, err = sess.sock.WriteTo(buf, sess.client); err != nil {
		return
	}
	return
}
