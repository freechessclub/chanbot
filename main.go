// Copyright © 2018 Free Chess Club <help@freechess.club>
//
// See license in LICENSE file
//

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/freechessclub/icsgo"
)

var (
	channels = []int{
		36, 39, 40,
	}
	ignoreList = []string{
		"ROBOadmin",
		"adminBOT",
	}
)

func main() {
	// create a new FICS client
	client, err := icsgo.NewClient(&icsgo.Config{
		DisableTimeseal: true,
	}, "freechess.org:5000", "ChanLogger", "")
	if err != nil {
		log.Fatalf("failed to create a new ICS client: %v", err)
		return
	}

	// add some delay to make sure that the server is ready to start accepting commands
	time.Sleep(2 * time.Second)

	// initialization commands here
	if err := client.Send("set interface www.freechess.club"); err != nil {
		log.Fatalf("failed to set interface: %v", err)
		return
	}

	if err := client.Send("set seek 0"); err != nil {
		log.Fatalf("failed to turn seek off: %v", err)
		return
	}

	for _, ch := range channels {
		if err := client.Send(fmt.Sprintf("+ch %d", ch)); err != nil {
			log.Printf("failed to add channel %d: %v", ch, err)
			continue
		}
	}

	// create db
	db, err := NewElasticDB("logs", "data")
	if err != nil {
		log.Fatalf("failed to create elastic DB: %v", err)
		return
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
				_, err := db.Put(msg)
				if err != nil {
					log.Printf("failed to put %v: %v", msg, err)
				}
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

				fields := strings.Fields(m.Message)
				var response string
				if len(fields) > 1 && fields[0] == "search" {
					query := map[string]interface{}{
						"user":    fields[1],
						"message": fields[1],
					}
					results, err := db.Search(query, 5)
					if err != nil {
						log.Printf("failed to search %v: %v", query, err)
					}
					if len(results) == 0 {
						response = "No results found for " + fields[1]
					} else {
						for _, result := range results {
							var ct icsgo.ChannelTell
							err := json.Unmarshal(*result.(*json.RawMessage), &ct)
							if err == nil {
								response += fmt.Sprintf(" [%s: %s] ", ct.User, ct.Message)
							}
						}
					}
				} else {
					response = "Hello " + m.User + ", I am ChanLogger. Looking for something? Type \"tell ChanLogger search [term]\""
				}
				client.Send("t " + m.User + " " + response)
			default:
				log.Printf("ignoring message: %v", msg)
			}
		}
	}
	client.Destroy()
}
