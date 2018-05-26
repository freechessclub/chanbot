// Copyright © 2018 Free Chess Club <help@freechess.club>
//
// See license in LICENSE file
//

package main

import (
	"io/ioutil"
	"net/http"
	"os"
	"runtime"

	"github.com/Sirupsen/logrus"
)

var (
	log = logrus.New()
)

func main() {
	resp, err := http.Get("http://myexternalip.com/raw")
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Stderr.WriteString("\n")
		os.Exit(1)
	}
	defer resp.Body.Close()

	ip, err := ioutil.ReadAll(resp.Body)
	user := "guest"
	pass := ""
	s, err := newSession(user, pass, string(ip))
	if err != nil {
		log.WithField("err", err).Println("Failed to create a new session")
		return
	}
	for {
		runtime.Gosched()
	}
	s.end()
}
