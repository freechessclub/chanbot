// Copyright © 2017 Free Chess Club <help@freechess.club>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/olivere/elastic"
	"github.com/ziutek/telnet"
)

const (
	loginPrompt    = "login:"
	passwordPrompt = "password:"
	newLine        = "\n"
	ficsPrompt     = "fics%"
)

type Session struct {
	conn     *telnet.Conn
	username string
}

type Msg struct {
	Channel string `json:"channel"`
	Handle  string `json:"handle"`
	Text    string `json:"text"`
}

var (
	chTellRE *regexp.Regexp
)

func init() {
	chTellRE = regexp.MustCompile(`(?s)^([a-zA-Z]+)(?:\([A-Z\*]+\))*\(([0-9]+)\):\s+(.*)`)
}

func Connect(network, addr, ip string, timeout, retries int) (*telnet.Conn, error) {
	ts := time.Duration(timeout) * time.Second

	var conn *telnet.Conn
	var connected bool = false
	var err error = nil

	for attempts := 1; attempts <= retries && connected != true; attempts++ {
		log.Printf("Connecting to chess server %s (attempt %d of %d)...", addr, attempts, retries)
		conn, err = telnet.DialTimeout(network, addr, ts)
		if err != nil {
			continue
		}
		connected = true
	}
	if err != nil || connected == false {
		return nil, fmt.Errorf("error connecting to server %s: %v", addr, err)
	}
	log.Printf("Connected!")

	conn.SetReadDeadline(time.Now().Add(ts))
	conn.SetWriteDeadline(time.Now().Add(ts))

	log.Printf("Registering IP: %s", "%i"+ip)
	send(conn, "%i"+ip)

	return conn, nil
}

func sanitize(b []byte) []byte {
	b = bytes.Replace(b, []byte("\u0007"), []byte{}, -1)
	b = bytes.Replace(b, []byte("\x00"), []byte{}, -1)
	b = bytes.Replace(b, []byte("\\   "), []byte{}, -1)
	b = bytes.Replace(b, []byte("\r"), []byte{}, -1)
	b = bytes.Replace(b, []byte("fics%"), []byte{}, -1)
	return bytes.TrimSpace(b)
}

func send(conn *telnet.Conn, cmd string) error {
	conn.SetWriteDeadline(time.Now().Add(20 * time.Second))
	buf := make([]byte, len(cmd)+1)
	copy(buf, cmd)
	buf[len(cmd)] = '\n'
	_, err := conn.Conn.Write(buf)
	return err
}

func readUntil(conn *telnet.Conn, delims ...string) ([]byte, error) {
	b, err := conn.ReadUntil(delims...)
	return sanitize(b), err
}

func sendAndReadUntil(conn *telnet.Conn, cmd string, delims ...string) ([]byte, error) {
	err := send(conn, cmd)
	if err != nil {
		return nil, err
	}
	return readUntil(conn, delims...)
}

func Login(conn *telnet.Conn, username, password string) (string, error) {
	var prompt string
	// guests have no passwords
	if username != "guest" && len(password) > 0 {
		prompt = passwordPrompt
	} else {
		prompt = "Press return to enter the server as"
		password = ""
	}

	// wait for the login prompt
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	readUntil(conn, loginPrompt)
	_, err := sendAndReadUntil(conn, username, prompt)
	if err != nil {
		return "", fmt.Errorf("Error creating new login session for %s: %v", username, err)
	}

	// wait for the password prompt
	out, err := sendAndReadUntil(conn, password, "****\n")
	if err != nil {
		return "", fmt.Errorf("Failed authentication for %s: %v", username, err)
	}

	log.Printf("Logging in as user %s", username)

	re := regexp.MustCompile("\\*\\*\\*\\* Starting FICS session as ([a-zA-Z]+)(?:\\(U\\))? \\*\\*\\*\\*")
	user := re.FindSubmatch(out)
	if user != nil && len(user) > 1 {
		username = string(user[1][:])
		log.Printf("Logged in as %s", username)
		return username, nil
	} else {
		return "", fmt.Errorf("Invalid password for %s", username)
	}
}

func (s *Session) ficsReader(client *elastic.Client) {
	for {
		s.conn.SetReadDeadline(time.Now().Add(3600 * time.Second))
		out, err := readUntil(s.conn, ficsPrompt)
		if err != nil {
			s.end()
			return
		}
		if len(out) == 0 {
			continue
		}

		msg, err := decodeMessage(out)
		if err != nil {
			log.Println("Error decoding message")
		}
		if msg == nil {
			continue
		}

		_, err = client.Index().
			Index("logs").
			Type("data").
			BodyJson(msg).
			Do()
		if err != nil {
			// Handle error
			panic(err)
		}
	}
}

func decodeMessage(msg []byte) (interface{}, error) {
	if msg == nil || bytes.Equal(msg, []byte("\n")) {
		return nil, nil
	}

	matches := chTellRE.FindSubmatch(msg)
	if matches != nil && len(matches) > 3 {
		return &Msg{
			Channel: string(matches[2][:]),
			Handle:  string(matches[1][:]),
			Text:    string(bytes.Replace(matches[3][:], []byte("\n"), []byte{}, -1)),
		}, nil
	}
	return nil, nil
}

func (s *Session) send(msg string) error {
	return send(s.conn, msg)
}

func newSession(user, pass, ip string) (*Session, error) {
	conn, err := Connect("tcp", "freechess.org:5000", ip, 5, 5)
	if err != nil {
		return nil, err
	}

	username, err := Login(conn, user, pass)
	if err != nil {
		return nil, err
	}

	_, err = sendAndReadUntil(conn, "set seek 0", newLine)
	if err != nil {
		return nil, err
	}

	_, err = sendAndReadUntil(conn, "set echo 1", newLine)
	if err != nil {
		return nil, err
	}

	_, err = sendAndReadUntil(conn, "set style 12", newLine)
	if err != nil {
		return nil, err
	}

	_, err = sendAndReadUntil(conn, "set interface www.freechess.club", newLine)
	if err != nil {
		return nil, err
	}

	s := &Session{
		conn:     conn,
		username: username,
	}

	client, err := elastic.NewClient(
		elastic.SetHealthcheck(false),
		elastic.SetSniff(false),
		elastic.SetURL(os.Getenv("SEARCHBOX_SSL_URL")))
	if err != nil {
		// Handle error
		panic(err)
	}

	exists, err := client.IndexExists("logs").Do()
	if err != nil {
		// Handle error
		panic(err)
	}
	if !exists {
		// Create a new index.
		createIndex, err := client.CreateIndex("logs").Do()
		if err != nil {
			// Handle error
			panic(err)
		}
		if !createIndex.Acknowledged {
			// Not acknowledged
		}
	}

	go s.ficsReader(client)
	return s, nil
}

func (s *Session) end() {
	send(s.conn, "exit")
	s.conn.Close()
}
