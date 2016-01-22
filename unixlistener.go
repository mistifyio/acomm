package acomm

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	logx "github.com/mistifyio/mistify-logrus-ext"
)

// UnixListener is a wrapper for a unix socket. It handles creation and
// listening for new connections, as well as graceful shutdown.
type UnixListener struct {
	alive     bool
	addr      *net.UnixAddr
	listener  *net.UnixListener
	waitgroup sync.WaitGroup
	stopChan  chan struct{}
	connChan  chan net.Conn
}

// NewUnixListener creates and initializes a new UnixListener.
func NewUnixListener(socketPath string) *UnixListener {
	// Ignore error since the only time it would arise is with a bad net
	// parameter
	addr, _ := net.ResolveUnixAddr("unix", socketPath)

	return &UnixListener{
		addr: addr,
	}
}

// Addr returns the string representation of the unix address.
func (ul *UnixListener) Addr() string {
	return ul.addr.String()
}

// URL returns the URL representation of the unix address.
func (ul *UnixListener) URL() *url.URL {
	u, _ := url.ParseRequestURI(fmt.Sprintf("unix://%s", ul.Addr()))
	return u
}

// Start prepares the listener and starts listening for new connections.
func (ul *UnixListener) Start() error {
	if ul.alive {
		return nil
	}

	ul.stopChan = make(chan struct{})
	ul.connChan = make(chan net.Conn, 1000)

	if err := ul.createListener(); err != nil {
		return err
	}

	ul.waitgroup.Add(1)
	go ul.listen()

	ul.alive = true

	return nil
}

// createListener creates a new net.UnixListener
func (ul *UnixListener) createListener() error {
	// create directory structure if it does not exist yet
	directory := filepath.Dir(ul.Addr())
	// TODO: Decide on permissions
	if err := os.MkdirAll(directory, os.ModePerm); err != nil {
		log.WithFields(log.Fields{
			"directory": directory,
			"perm":      os.ModePerm,
			"error":     err,
		}).Error("failed to create directory for socket")
		return err
	}

	listener, err := net.ListenUnix("unix", ul.addr)
	if err != nil {
		log.WithFields(log.Fields{
			"addr":  ul.Addr(),
			"error": err,
		}).Error("failed to create response listener")
		return err
	}

	ul.listener = listener
	return nil
}

// listen continuously listens for new connections
func (ul *UnixListener) listen() {
	defer ul.waitgroup.Done()
	defer logx.LogReturnedErr(ul.listener.Close, log.Fields{
		"addr": ul.Addr(),
	}, "failed to close listener")

	for {
		select {
		case <-ul.stopChan:
			log.WithFields(log.Fields{
				"addr": ul.Addr(),
			}).Info("stop listening")
			return
		default:
		}

		if err := ul.listener.SetDeadline(time.Now().Add(time.Second)); err != nil {
			log.WithFields(log.Fields{
				"addr":  ul.Addr(),
				"error": err,
			}).Error("failed to set listener deadline")
		}

		conn, err := ul.listener.Accept()
		if nil != err {
			// Don't worry about a timeout
			if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
				continue
			}

			log.WithFields(log.Fields{
				"addr":  ul.Addr(),
				"error": err,
			}).Error("failed to accept new connection")
			continue
		}

		ul.waitgroup.Add(1)
		ul.connChan <- conn
	}
}

// Stop stops listening for new connections. It blocks until existing
// connections are handled and the listener closed.
func (ul *UnixListener) Stop() {
	if !ul.alive {
		return
	}

	close(ul.stopChan)
	ul.waitgroup.Wait()

	ul.alive = false
	return
}

// NextConn blocks and returns the next connection. It will return nil when the
// listener is stopped and all existing connections have been handled.
// Connections should be handled in a go routine to take advantage of
// concurrency. When done, the connection MUST be finished with a call to
// DoneConn.
func (ul *UnixListener) NextConn() net.Conn {
	select {
	case <-ul.stopChan:
		return nil
	case conn := <-ul.connChan:
		return conn
	}
}

// DoneConn completes the handling of a connection.
func (ul *UnixListener) DoneConn(conn net.Conn) {
	if conn == nil {
		return
	}

	defer ul.waitgroup.Done()
	defer logx.LogReturnedErr(conn.Close,
		log.Fields{
			"addr": ul.addr,
		}, "failed to close unix connection",
	)
}