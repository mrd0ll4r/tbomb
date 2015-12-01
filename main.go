package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// global transactionID, infohash and peerID counters
var (
	tid *uint32
	ih  *uint64
	pid *uint64
)

//flags
var (
	timeout        int
	requests       int64
	clients        int
	target         string
	keepAlive      bool
	scrapeMode     bool
	errorReporting bool
)

type Configuration struct {
	url        string
	requests   int64
	period     time.Duration
	keepAlive  bool
	scrapeMode bool
}

type Result struct {
	connectAttempts int64
	failedConnects  int64
	requests        int64
	success         int64
	failed          int64
	errors          map[string]int64
}

type Client struct {
	conn     net.Conn
	result   *Result
	infohash *uint64
	peerId   *uint64
	transId  *uint32
}

func init() {
	flag.IntVar(&timeout, "t", 0, "Period of testing (in seconds)")
	flag.Int64Var(&requests, "r", -1, "Number of requests per client, -1=unlimited")
	flag.IntVar(&clients, "c", 100, "Number of concurrent clients")
	flag.StringVar(&target, "u", "", "Target URL (e.g. 127.0.0.1:12345 or tracker.example.org:1337)")
	flag.BoolVar(&keepAlive, "k", false, "Re-use connection IDs")
	flag.BoolVar(&scrapeMode, "s", false, "Scrape instead of announcing")
	flag.BoolVar(&errorReporting, "e", false, "Enable detailed error reporting")

	var infoh uint64 = uint64(rand.Int63())
	ih = &infoh

	var peerid uint64 = uint64(rand.Int63())
	pid = &peerid

	var transid uint32 = rand.Uint32()
	tid = &transid
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	c := newConfig()
	t := make(chan struct{})
	if c.period > 0 {
		go func() {
			<-time.After(c.period)
			close(t)
		}()
	}

	wg := &sync.WaitGroup{}
	results := make([]*Result, clients)

	fmt.Printf("Dispatching %d clients\n", clients)
	wg.Add(clients)
	startTime := time.Now()
	for i := 0; i < clients; i++ {
		result := &Result{
			errors: make(map[string]int64),
		}
		results[i] = result

		conn, err := net.Dial("udp4", c.url)
		if err != nil {
			panic(err)
		}

		client := &Client{
			conn:     conn,
			result:   result,
			infohash: ih,
			peerId:   pid,
			transId:  tid,
		}

		go client.do(c, t, wg)
	}

	fmt.Println("Waiting for results...")
	wg.Wait()
	printResults(results, startTime)
}

func newConfig() *Configuration {
	if target == "" {
		flag.Usage()
		os.Exit(1)
	}

	if requests <= 0 && timeout <= 0 {
		flag.Usage()
		os.Exit(1)
	}

	if requests > 0 && timeout > 0 {
		flag.Usage()
		os.Exit(1)
	}

	configuration := &Configuration{
		url:        target,
		requests:   int64((1 << 63) - 1),
		period:     time.Duration(0) * time.Second,
		keepAlive:  keepAlive,
		scrapeMode: scrapeMode,
	}

	if timeout > 0 {
		configuration.period = time.Duration(timeout) * time.Second
	}

	if requests != -1 {
		configuration.requests = requests
	}

	return configuration
}

func printResults(results []*Result, startTime time.Time) {
	var requests int64
	var success int64
	var failed int64
	var connectAttempts int64
	var failedConnects int64
	errors := make(map[string]int64)

	for _, result := range results {
		requests += result.requests
		success += result.success
		failed += result.failed
		connectAttempts += result.connectAttempts
		failedConnects += result.failedConnects
		if errorReporting {
			for err, count := range result.errors {
				_, exists := errors[err]
				if !exists {
					errors[err] = 0
				}
				errors[err] += count
			}
		}
	}

	elapsed := time.Since(startTime).Seconds()

	if elapsed == 0 {
		elapsed = 1
	}

	fmt.Println()
	fmt.Printf("Requests:                       %10d\n", requests)
	fmt.Printf("Successful requests:            %10d\n", success)
	fmt.Printf("failed requests:                %10d\n", failed)
	fmt.Printf("Connect attempts:               %10d\n", connectAttempts)
	fmt.Printf("Failed connects:                %10d\n", failedConnects)
	fmt.Printf("Successful requests rate:       %10.0f hits/sec\n", float64(success)/elapsed)
	fmt.Printf("Test time:                      %10.2f sec\n", elapsed)
	if errorReporting {
		fmt.Println("Encountere errors:")
		for err, count := range errors {
			fmt.Printf("    %30s  %10d\n", err, count)
		}
	}
}

func (r *Result) incrementError(err string) {
	if errorReporting {
		_, exists := r.errors[err]
		if !exists {
			r.errors[err] = 0
		}
		r.errors[err]++
	}
}

func (c *Client) do(conf *Configuration, t chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	//prepare packet
	packet, err := prepareAnnounce()
	if err != nil {
		panic(fmt.Sprintf("Unable to create packet: %s", err.Error()))
	}

	//prepare a receive buffer
	buf := make([]byte, 1024)

	//get a transaction ID
	transactionID := atomic.AddUint32(c.transId, 1)

	var connId uint64
	var connIdExpires <-chan time.Time

	//perform requests
loop:
	for c.result.requests < conf.requests {
		//check if time has expired
		select {
		case <-t:
			return
		default:
		}

		c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))

		//check if we have to (re)connect
		if conf.keepAlive {
			if connIdExpires == nil {
				//initial connect or fast reconnect
				transactionID = atomic.AddUint32(c.transId, 1)
				connId, err = c.connect(transactionID)
				if err != nil {
					c.result.incrementError(err.Error())
					continue loop
				}
				connIdExpires = time.After(time.Minute)
			} else {
				select {
				case <-connIdExpires:
					//reconnect
					transactionID = atomic.AddUint32(c.transId, 1)
					connId, err = c.connect(transactionID)
					if err != nil {
						c.result.incrementError(err.Error())
						connIdExpires = nil //we want to try this again ASAP
						continue loop
					}
					connIdExpires = time.After(time.Minute)
				default: //still valid
				}
			}
		} else {
			//reconnect
			transactionID = atomic.AddUint32(c.transId, 1)
			connId, err = c.connect(transactionID)
			if err != nil {
				c.result.incrementError(err.Error())
				continue loop
			}
		}

		transactionID = atomic.AddUint32(c.transId, 1)
		infohash := atomic.AddUint64(c.infohash, 1)
		peerId := atomic.AddUint64(c.peerId, 1)

		err = c.announce(buf, packet, transactionID, connId, infohash, peerId)
		c.result.requests++
		if err != nil {
			c.result.incrementError(err.Error())
			c.result.failed++
		} else {
			c.result.success++
		}
	}
}

func prepareAnnounce() ([]byte, error) {
	bbuf := bytes.NewBuffer(nil)

	// connection id
	_, err := bbuf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// action
	_, err = bbuf.Write([]byte{0, 0, 0, 0x01})
	if err != nil {
		return nil, err
	}

	// transaction ID
	_, err = bbuf.Write([]byte{0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// infohash
	_, err = bbuf.WriteString("xxxxxxxxxxxxxxxxxxxx")
	if err != nil {
		return nil, err
	}

	// peer ID
	_, err = bbuf.WriteString("xxxxxxxxxxxxxxxxxxxx")
	if err != nil {
		return nil, err
	}

	// downloaded
	_, err = bbuf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// left
	_, err = bbuf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0x01})
	if err != nil {
		return nil, err
	}

	// uploaded
	_, err = bbuf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// event
	_, err = bbuf.Write([]byte{0, 0, 0, 0x02})
	if err != nil {
		return nil, err
	}

	// IP Address
	_, err = bbuf.Write([]byte{0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// key (?)
	_, err = bbuf.Write([]byte{0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// numwant
	_, err = bbuf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	if err != nil {
		return nil, err
	}

	// port
	_, err = bbuf.Write([]byte{0x05, 0x00})
	if err != nil {
		return nil, err
	}

	return bbuf.Bytes(), nil
}

func (c *Client) announce(buf, packet []byte, transactionID uint32, connectionID, infohash, peerID uint64) error {
	binary.BigEndian.PutUint64(packet[0:8], connectionID)
	binary.BigEndian.PutUint32(packet[12:16], transactionID)

	infohashString := fmt.Sprintf("%020x", infohash)
	peerIdString := fmt.Sprintf("%020x", peerID)

	for i := 0; i < 20; i++ {
		packet[16+i] = infohashString[i] //put infohash
		packet[36+i] = peerIdString[i]   //put peerID
	}

	//send
	n, err := c.conn.Write(packet)
	if err != nil {
		return err
	}
	if n != 98 {
		return errors.New("announce: Did not send 98 bytes")
	}

	//receive response
	n, err = c.conn.Read(buf)
	if err != nil {
		if strings.HasSuffix(err.Error(), "i/o timeout") {
			return errors.New("announce: I/O timeout on receive")
		}
		return fmt.Errorf("announce: %s", err)
	}
	if n < 20 {
		return errors.New("announce: Did not receive at least 20 bytes")
	}

	// parse action
	action := binary.BigEndian.Uint32(buf[:4])
	if action != 1 {
		if action == 3 {
			errVal := string(buf[8:n])
			return fmt.Errorf("announce: tracker responded with error: %s", errVal)
		}
		return errors.New("announce: tracker responded with action != 1")
	}

	transID := binary.BigEndian.Uint32(buf[4:8])
	if transID != transactionID {
		return errors.New("announce: transaction IDs do not match")
	}

	return nil
}

func (c *Client) connect(transactionID uint32) (u uint64, err error) {
	c.result.connectAttempts++
	defer func() {
		if err != nil {
			c.result.failedConnects++
		}
	}()
	buf := make([]byte, 16)

	buf[2] = 0x04
	buf[3] = 0x17
	buf[4] = 0x27
	buf[5] = 0x10
	buf[6] = 0x19
	buf[7] = 0x80

	binary.BigEndian.PutUint32(buf[12:16], transactionID)

	n, err := c.conn.Write(buf)
	if err != nil {
		return 0, err
	}

	if n != 16 {
		return 0, errors.New("connect: Did not send 16 bytes")
	}

	buf = make([]byte, 64)
	n, err = c.conn.Read(buf)
	if err != nil {
		if strings.HasSuffix(err.Error(), "i/o timeout") {
			return 0, errors.New("connect: I/O timeout on receive")
		}
		return 0, fmt.Errorf("connect: %s", err)
	}

	if n != 16 {
		return 0, errors.New("connect: Did not receive 16 bytes")
	}

	b := buf[:n]

	action := binary.BigEndian.Uint32(b[:4])
	if action != 0 {
		return 0, errors.New("connect: action != 0")
	}

	transID := binary.BigEndian.Uint32(b[4:8])
	if transID != transactionID {
		return 0, errors.New("connect: transaction IDs do not match")
	}

	connID := binary.BigEndian.Uint64(b[8:16])
	return connID, nil
}
