// Copyright © 2019 Free Chess Club <help@freechess.club>
//
// See license in LICENSE file
//

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/olivere/elastic"
)

// ElasticDB represents an Elasticsearch data store
type ElasticDB struct {
	client *elastic.Client
	index  string
	typ    string
}

// NewElasticDB creates a new elastic search index
func NewElasticDB(index, typ string) (DB, error) {
	// create a new elasticsearch client
	client, err := elastic.NewClient(
		elastic.SetHealthcheck(false),
		elastic.SetSniff(false),
		elastic.SetURL(os.Getenv("SEARCHBOX_SSL_URL")))
	if err != nil {
		return nil, fmt.Errorf("failed to create a new elasticsearch client: %v", err)
	}

	// check if specified index exists or not
	exists, err := client.IndexExists(index).Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to check if index %s exists: %v", index, err)
	}

	if !exists {
		// create a new index
		createIndex, err := client.CreateIndex(index).Do(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to create index %s: %v", index, err)
		}
		if !createIndex.Acknowledged {
			// not acknowledged
		}
	}

	return &ElasticDB{
		client: client,
		index:  index,
		typ:    typ,
	}, nil
}

// Put puts the given message into the elastic store
func (e *ElasticDB) Put(msg interface{}) (string, error) {
	put, err := e.client.Index().
		Index(e.index).
		Type(e.typ).
		BodyJson(msg).
		Do(context.Background())
	if err != nil {
		return "", fmt.Errorf("error indexing message %v: %v", msg, err)
	}
	return put.Id, nil
}

// Get gets a message from the elastic store
func (e *ElasticDB) Get(id string) (interface{}, error) {
	get, err := e.client.Get().
		Index(e.index).
		Type(e.typ).
		Id(id).
		Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("error getting message with id %s: %v", id, err)
	}

	return get.Source, nil
}

// Search searches the elastic data store, given a query map
func (e *ElasticDB) Search(queryMap map[string]interface{}, count int) ([]interface{}, error) {
	query := elastic.NewBoolQuery()
	for k, v := range queryMap {
		query = query.Should(elastic.NewTermQuery(k, v))
	}
	result, err := e.client.Search().
		Index(e.index).
		Type(e.typ).
		Query(query).
		From(0).Size(count).
		Pretty(true).
		Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("error searching with query %v: %v", queryMap, err)
	}

	var out []interface{}
	for _, hit := range result.Hits.Hits {
		out = append(out, hit.Source)
	}
	return out, nil
}
