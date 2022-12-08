package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/RussellLuo/go-rocketqa"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/knnsearch"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"golang.org/x/exp/slices"
)

type Candidate struct {
	Title string
	Para  string

	Score float32
}

type Querier struct {
	es *elasticsearch.Client
	de *rocketqa.DualEncoder
	ce *rocketqa.CrossEncoder
}

func NewQuerier(es *elasticsearch.Client, de *rocketqa.DualEncoder, ce *rocketqa.CrossEncoder) *Querier {
	return &Querier{
		es: es,
		de: de,
		ce: ce,
	}
}

func (q *Querier) Search(index, query string) []*Candidate {
	vector := q.de.EncodeQuery(query).Unitize().ToFloat64()

	ks := knnsearch.New(q.es)
	ks.Index(index).Request(&knnsearch.Request{
		Knn: types.CoreKnnQuery{
			Field:         "vector",
			QueryVector:   vector,
			K:             10,
			NumCandidates: 100,
		},
	})
	resp, err := ks.Do(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	var v map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		log.Fatal(err)
	}

	var candidates []*Candidate

	if v["hits"] == nil {
		return candidates
	}

	hits := v["hits"].(map[string]interface{})["hits"].([]interface{})
	for _, h := range hits {
		doc := h.(map[string]interface{})
		source := doc["_source"].(map[string]interface{})
		candidates = append(candidates, &Candidate{
			Title: source["title"].(string),
			Para:  source["paragraph"].(string),
		})
	}

	return candidates
}

func (q *Querier) Sort(query string, candidates []*Candidate) {
	for _, c := range candidates {
		c.Score = q.ce.Rank(query, c.Para, "")
	}
	slices.SortFunc(candidates, func(a, b *Candidate) bool {
		return a.Score > b.Score
	})
}

func main() {
	var indexName string
	flag.StringVar(&indexName, "index", "", "The index name")
	flag.Parse()

	if indexName == "" {
		log.Fatalf(`argument "index" is required`)
	}

	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{"https://localhost:9200"},
		Username:  "elastic",
		Password:  "123456",
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	de, err := rocketqa.NewDualEncoder(&rocketqa.DualEncoderConfig{
		ModelPath:         "../../../testdata/zh_dureader_de_v2.pdmodel",
		ParamsPath:        "../../../testdata/zh_dureader_de_v2.pdiparams",
		VocabFile:         "../../../testdata/zh_vocab.txt",
		DoLowerCase:       true,
		QueryMaxSeqLength: 32,
		ParaMaxSeqLength:  384,
		ForCN:             true,
	})
	if err != nil {
		log.Fatal(err)
	}

	ce, err := rocketqa.NewCrossEncoder(&rocketqa.CrossEncoderConfig{
		ModelPath:    "../../../testdata/zh_dureader_ce_v2.pdmodel",
		ParamsPath:   "../../../testdata/zh_dureader_ce_v2.pdiparams",
		VocabFile:    "../../../testdata/zh_vocab.txt",
		DoLowerCase:  true,
		MaxSeqLength: 384,
		ForCN:        true,
	})
	if err != nil {
		log.Fatal(err)
	}

	querier := NewQuerier(es, de, ce)
	fmt.Print("Query: ")

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		query := scanner.Text()

		candidates := querier.Search(indexName, query)
		fmt.Println("Candidates:")
		for _, c := range candidates {
			fmt.Printf("- %s\n", c.Para)
		}

		fmt.Println("Answers:")
		querier.Sort(query, candidates)
		for i, c := range candidates {
			fmt.Printf("%d %s\n", i, c.Para)
		}

		fmt.Print("Query: ")
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}