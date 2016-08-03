package main

import (
	"bytes"
	"crypto/sha1"
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

func init() {
	flag.IntVar(&timeout, "t", 0, "Period of testing (in seconds)")
	flag.Int64Var(&requests, "r", -1, "Number of requests per client, -1=unlimited")
	flag.IntVar(&clients, "c", 100, "Number of concurrent clients")
	flag.StringVar(&target, "u", "", "Target URL (e.g. 127.0.0.1:12345 or tracker.example.org:1337)")
	flag.BoolVar(&keepAlive, "k", false, "Re-use connection IDs")
	flag.BoolVar(&scrapeMode, "s", false, "Scrape instead of announcing")
	flag.BoolVar(&errorReporting, "e", false, "Enable detailed error reporting")

	var infoh = uint64(rand.Int63())
	ih = &infoh

	var peerid = uint64(rand.Int63())
	pid = &peerid

	var transid = rand.Uint32()
	tid = &transid
}

// Global transactionID, infohash and peerID counters.
var (
	tid *uint32
	ih  *uint64
	pid *uint64
)

// Flags.
var (
	timeout        int
	requests       int64
	clients        int
	target         string
	keepAlive      bool
	scrapeMode     bool
	errorReporting bool
)

const readTimeout time.Duration = time.Second * 2

type configuration struct {
	url        string
	requests   int64
	period     time.Duration
	keepAlive  bool
	scrapeMode bool
}

type result struct {
	connectAttempts int64
	failedConnects  int64
	requests        int64
	success         int64
	failed          int64
	errors          map[string]int64
}

type client struct {
	conn          net.Conn
	result        *result
	infohash      *uint64
	peerID        *uint64
	transactionID *uint32
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
	results := make([]*result, clients)

	fmt.Printf("Dispatching %d clients\n", clients)
	wg.Add(clients)
	startTime := time.Now()
	for i := 0; i < clients; i++ {
		result := &result{
			errors: make(map[string]int64),
		}
		results[i] = result

		conn, err := net.Dial("udp", c.url)
		if err != nil {
			panic(err)
		}

		client := &client{
			conn:          conn,
			result:        result,
			infohash:      ih,
			peerID:        pid,
			transactionID: tid,
		}

		go client.do(c, t, wg)
		go func() {
			if c.period > 0 {
				<-t
				conn.SetReadDeadline(time.Now())
			}
		}()
	}

	fmt.Println("Waiting for results...")
	wg.Wait()
	printResults(results, time.Since(startTime), c)
}

func newConfig() *configuration {
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

	configuration := &configuration{
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

func printResults(results []*result, runTime time.Duration, c *configuration) {
	var requests int64
	var success int64
	var failed int64
	var connectAttempts int64
	var failedConnects int64
	var timeouts int64
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

				if strings.Contains(err, "I/O timeout") {
					timeouts += count
				}
			}
		}
	}

	elapsed := runTime.Seconds()

	if elapsed == 0 {
		elapsed = 1
	}

	//compute concurrency
	workTime := float64(clients) * elapsed
	realTime := workTime - (float64(timeouts) * readTimeout.Seconds())
	concurrency := float64(clients) * (realTime / workTime)

	fmt.Println()
	fmt.Printf("Requests:                       %10d\n", requests)
	fmt.Printf("Successful requests:            %10d\n", success)
	fmt.Printf("failed requests:                %10d\n", failed)
	fmt.Printf("Connect attempts:               %10d\n", connectAttempts)
	fmt.Printf("Failed connects:                %10d\n", failedConnects)
	fmt.Printf("Successful requests rate:       %10.0f hits/sec\n", float64(success)/elapsed)
	if c.period > 0 {
		fmt.Printf("Approximate concurrency:        %10.2f clients running\n", concurrency)
	}
	fmt.Printf("Test time:                      %10.2f sec\n", elapsed)
	if errorReporting {
		fmt.Println("Errors encountered:")
		for err, count := range errors {
			fmt.Printf("    %30s  %10d\n", err, count)
		}
	}
}

func (r *result) incrementError(err string, t chan struct{}) {
	if errorReporting {
		if strings.Contains(err, "I/O timeout") {
			select {
			case <-t:
				//timeout because we're done
				return
			default:
			}
		}
		_, exists := r.errors[err]
		if !exists {
			r.errors[err] = 0
		}
		r.errors[err]++
	}
}

func (c *client) do(conf *configuration, t chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	// Prepare a packet.
	packet, err := prepareAnnounce()
	if err != nil {
		panic(fmt.Sprintf("Unable to create packet: %s", err.Error()))
	}

	// Prepare a receive buffer.
	buf := make([]byte, 1024)

	// Get a transaction ID.
	transactionID := atomic.AddUint32(c.transactionID, 1)

	var connectionID uint64
	var connectionIDExpired <-chan time.Time

	// Perform requests.
loop:
	for c.result.requests < conf.requests {
		// Check if time has expired.
		select {
		case <-t:
			return
		default:
		}

		// Check if we have to (re)connect.
		if conf.keepAlive {
			if connectionIDExpired == nil {
				// Initial connect or fast reconnect.
				transactionID = atomic.AddUint32(c.transactionID, 1)
				connectionID, err = c.connect(transactionID)
				if err != nil {
					c.result.incrementError(err.Error(), t)
					continue loop
				}
				connectionIDExpired = time.After(time.Minute)
			} else {
				select {
				case <-connectionIDExpired:
					// Reconnect.
					transactionID = atomic.AddUint32(c.transactionID, 1)
					connectionID, err = c.connect(transactionID)
					if err != nil {
						c.result.incrementError(err.Error(), t)
						connectionIDExpired = nil // Do a fast reconnect.
						continue loop
					}
					connectionIDExpired = time.After(time.Minute)
				default:
				}
			}
		} else {
			// Reconnect on every request.
			transactionID = atomic.AddUint32(c.transactionID, 1)
			connectionID, err = c.connect(transactionID)
			if err != nil {
				c.result.incrementError(err.Error(), t)
				continue loop
			}
		}

		transactionID = atomic.AddUint32(c.transactionID, 1)
		infohash := atomic.AddUint64(c.infohash, 1)
		peerID := atomic.AddUint64(c.peerID, 1)

		err = c.announce(buf, packet, transactionID, connectionID, infohash, peerID)
		c.result.requests++
		if err != nil {
			c.result.incrementError(err.Error(), t)
			c.result.failed++
		} else {
			c.result.success++
		}
	}
}

func prepareAnnounce() ([]byte, error) {
	bbuf := bytes.NewBuffer(nil)

	// Connection id
	_, err := bbuf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// Action
	_, err = bbuf.Write([]byte{0, 0, 0, 0x01})
	if err != nil {
		return nil, err
	}

	// Transaction ID
	_, err = bbuf.Write([]byte{0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// Infohash
	_, err = bbuf.WriteString("xxxxxxxxxxxxxxxxxxxx")
	if err != nil {
		return nil, err
	}

	// Peer ID
	_, err = bbuf.WriteString("xxxxxxxxxxxxxxxxxxxx")
	if err != nil {
		return nil, err
	}

	// Downloaded
	_, err = bbuf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// Left
	_, err = bbuf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0x01})
	if err != nil {
		return nil, err
	}

	// Uploaded
	_, err = bbuf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// Event
	_, err = bbuf.Write([]byte{0, 0, 0, 0x02})
	if err != nil {
		return nil, err
	}

	// IP Address
	_, err = bbuf.Write([]byte{0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// "Key"
	_, err = bbuf.Write([]byte{0, 0, 0, 0})
	if err != nil {
		return nil, err
	}

	// Numwant
	_, err = bbuf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	if err != nil {
		return nil, err
	}

	// Port
	_, err = bbuf.Write([]byte{0x05, 0x00})
	if err != nil {
		return nil, err
	}

	return bbuf.Bytes(), nil
}

func (c *client) announce(buf, packet []byte, transactionID uint32, connectionID, infohash, peerID uint64) error {
	c.conn.SetReadDeadline(time.Now().Add(readTimeout))

	binary.BigEndian.PutUint64(packet[0:8], connectionID)
	binary.BigEndian.PutUint32(packet[12:16], transactionID)

	infohashBytes := sha1.Sum([]byte{byte(infohash >> 56),
		byte(infohash >> 48),
		byte(infohash >> 40),
		byte(infohash >> 32),
		byte(infohash >> 24),
		byte(infohash >> 16),
		byte(infohash >> 8),
		byte(infohash)})

	peerIDString := fmt.Sprintf("%020x", peerID)

	for i := 0; i < 20; i++ {
		packet[16+i] = infohashBytes[i]
		packet[36+i] = peerIDString[i]
	}

	// Send announce.
	n, err := c.conn.Write(packet)
	if err != nil {
		return err
	}
	if n != 98 {
		return errors.New("announce: Did not send 98 bytes")
	}

	// Receive response.
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

	// Parse action.
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

func (c *client) connect(transactionID uint32) (u uint64, err error) {
	c.conn.SetReadDeadline(time.Now().Add(readTimeout))

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
