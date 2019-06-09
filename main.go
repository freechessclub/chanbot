// Copyright © 2018 Free Chess Club <help@freechess.club>
//
// See license in LICENSE file
//

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

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

	// initialization commands here
	client.Send("set seek 0")
	client.Send("set echo 1")
	client.Send("set interface www.freechess.club")
	for _, ch := range channels {
		client.Send(fmt.Sprintf("+ch %d", ch))
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
					if m.Handle == user {
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
						"handle":  fields[1],
						"message": fields[1],
					}
					results, err := db.Search(query, 5)
					if err != nil {
						log.Printf("failed to search %v: %v", query, err)
					}
					log.Printf("RESULTS::%s", results)
					if len(results) == 0 {
						response = "No results found for " + fields[1]
					} else {
						for _, result := range results {
							r := result.(*icsgo.ChannelTell)
							response += fmt.Sprintf(" [%s: %s] ", r.Handle, r.Message)
						}
					}
				} else {
					response = "Hello " + m.Handle + ", I am ChanLogger. Looking for something? Type \"tell ChanLogger search [term]\""
				}
				client.Send("t " + m.Handle + " " + response)
			}
		}
	}
	client.Destroy()
}
