// Copyright © 2018 Free Chess Club <help@freechess.club>
//
// See license in LICENSE file
//

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/freechessclub/icsgo"
	"github.com/gorilla/websocket"
	"github.com/smallnest/ringbuffer"
)

var (
	channels = []int{
		36, 39, 40,
	}
	ignoreList = []string{
		"ROBOadmin",
		"adminBOT",
	}
	addr       = flag.String("addr", ":80", "http service address")
	ringBuffer = ringbuffer.New(1048576)
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 5 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 4096

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Poll for messages with this period.
	msgPeriod = 5 * time.Second
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  maxMessageSize,
	WriteBufferSize: maxMessageSize,
}

// MsgTimestamp represents a timestampped message
type MsgTimestamp struct {
	*icsgo.ChannelTell
	Timestamp time.Time `json:"timestamp"`
}

func writePump(ws *websocket.Conn, readPtr int) {
	pingticker := time.NewTicker(pingPeriod)
	msgticker := time.NewTicker(msgPeriod)
	defer func() {
		pingticker.Stop()
		msgticker.Stop()
		ws.Close()
	}()

	for {
		select {
		case <-msgticker.C:
			size := ringBuffer.Length()
			if size > readPtr {
				ws.SetWriteDeadline(time.Now().Add(writeWait))
				bytes := ringBuffer.Bytes()
				if err := ws.WriteMessage(websocket.TextMessage, bytes[readPtr:]); err != nil {
					return
				}
				readPtr += size
			}
		case <-pingticker.C:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}
	readPtr := 0
	go writePump(ws, readPtr)
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.ServeFile(w, r, "home.html")
}

func main() {
	flag.Parse()
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", serveWs)
	server := &http.Server{
		Addr:              *addr,
		ReadHeaderTimeout: 3 * time.Second,
	}
	go server.ListenAndServe()

	// create a new FICS client
	client, err := icsgo.NewClient(&icsgo.Config{
		DisableTimeseal: true,
	}, "freechess.org:5000", "ChanLogger", "")
	if err != nil {
		log.Fatalf("failed to create a new ICS client: %v", err)
		return
	}

	// add some delay to make sure that the server is ready to start accepting commands
	time.Sleep(3 * time.Second)

	// initialization commands here
	if err := client.Send([]byte("set seek 0")); err != nil {
		log.Fatalf("failed to turn seek off: %v", err)
		return
	}

	for _, ch := range channels {
		if err := client.Send([]byte(fmt.Sprintf("+ch %d", ch))); err != nil {
			log.Printf("failed to add channel %d: %v", ch, err)
			continue
		}
	}

	// handle interrupts
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		client.Destroy()
		os.Exit(1)
	}()

	for {
		msgs, err := client.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("error receiving server output: %v", err)
			break
		}
		if msgs == nil {
			continue
		}

		for _, msg := range msgs {
			switch msg.(type) {
			case *icsgo.ChannelTell:
				m := msg.(*icsgo.ChannelTell)
				t := time.Now()
				tell := []byte(t.Format("15:04:05") + "(" + m.Channel + ") " + m.User + ": " + m.Message + "\n")
				ringBuffer.Write(tell)
			case *icsgo.PrivateTell:
				m := msg.(*icsgo.PrivateTell)
				ignoreTell := false
				for _, user := range ignoreList {
					if m.User == user {
						ignoreTell = true
						break
					}
				}
				if ignoreTell {
					continue
				}
				response := "Hello " + m.User + ", I am ChanLogger. Looking for something?"
				client.Send([]byte("t " + m.User + " " + response))
			}
		}
	}
	client.Destroy()
}
