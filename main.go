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
	"path/filepath"
	"syscall"
	"time"

	"github.com/freechessclub/icsgo"
	"github.com/gorilla/websocket"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	channels = []int{
		36, 39, 40,
	}
	ignoreList = []string{
		"ROBOadmin",
		"adminBOT",
	}
	addr    = flag.String("addr", ":8080", "http service address")
	logFile = "chat.log"
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

func readFileIfModified(lastMod time.Time, filename string) ([]byte, time.Time, error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return nil, lastMod, err
	}
	if !fi.ModTime().After(lastMod) {
		return nil, lastMod, nil
	}

	p, err := os.ReadFile(filepath.Clean(filename))
	if err != nil {
		return nil, fi.ModTime(), err
	}
	return p, fi.ModTime(), nil
}

func reader(ws *websocket.Conn) {
	defer ws.Close()
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			break
		}
	}
}

func writer(ws *websocket.Conn, lastMod time.Time, seek int) {
	lastError := ""
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
			var p []byte
			var err error

			p, lastMod, err = readFileIfModified(lastMod, logFile)
			if err != nil {
				if s := err.Error(); s != lastError {
					lastError = s
					p = []byte(lastError)
				}
			} else {
				lastError = ""
			}

			if p != nil {
				size := len(p)
				if size > seek {
					ws.SetWriteDeadline(time.Now().Add(writeWait))
					if err := ws.WriteMessage(websocket.TextMessage, p[seek:]); err != nil {
						return
					}
					seek = size
				}
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
		panic(fmt.Sprintln("upgrade:", err))
	}

	lastMod := time.Unix(0, 0)
	seek := 0
	go writer(ws, lastMod, seek)
	reader(ws)
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
	http.Handle("/css/", http.StripPrefix("/css/", http.FileServer(http.Dir("./css"))))
	http.HandleFunc("/ws", serveWs)
	server := &http.Server{
		Addr:              *addr,
		ReadHeaderTimeout: 3 * time.Second,
	}
	go server.ListenAndServe()

	// create a new FICS client
	client, err := icsgo.NewClient(&icsgo.Config{
		DisableTimeseal: true,
	}, "freechess.org:5000", "chanbot", "")
	if err != nil {
		panic(fmt.Sprintf("failed to create a new ICS client: %v", err))
	}

	// add some delay to make sure that the server is ready to start accepting commands
	time.Sleep(3 * time.Second)

	// initialization commands here
	if err := client.Send([]byte("set seek 0")); err != nil {
		panic(fmt.Sprintf("failed to turn seek off: %v", err))
	}

	if err := client.Send([]byte("set 1 I am chanbot. See my logs at https://chanbot.freechess.club/")); err != nil {
		panic(fmt.Sprintf("failed to set note 1: %v", err))
	}

	for _, ch := range channels {
		if err := client.Send([]byte(fmt.Sprintf("+ch %d", ch))); err != nil {
			panic(fmt.Sprintf("failed to add channel %d: %v", ch, err))
		}
	}

	logger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    1, // megabytes
		MaxBackups: 30,
		MaxAge:     30,   //days
		Compress:   true, // disabled by default
	}
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetOutput(logger)

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
			panic(fmt.Sprintf("error receiving server output: %v", err))
		}
		if msgs == nil {
			continue
		}

		for _, msg := range msgs {
			switch msg.(type) {
			case *icsgo.ChannelTell:
				m := msg.(*icsgo.ChannelTell)
				log.Println("(" + m.Channel + ") " + m.User + ": " + m.Message)
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
				response := "Hello " + m.User + ", I am chanbot. See my logs at https://chanbot.freechess.club/"
				client.Send([]byte("t " + m.User + " " + response))
			}
		}
	}
	client.Destroy()
}
