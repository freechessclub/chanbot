// Copyright © 2019 Free Chess Club <help@freechess.club>
//
// See license in LICENSE file
//

package main

// DB represents the interface for chanbot data
type DB interface {
	Put(msg interface{}) (string, error)
	Get(id string) (interface{}, error)
	Search(queryMap map[string]interface{}, count int) ([]interface{}, error)
}
