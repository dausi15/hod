//go:generate msgp
//msgp:ignore LinkResultMap
package db

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gtfierro/hod/turtle"

	"github.com/mitghi/btree"
)

var emptyResultMapList = []ResultMap{}
var emptyLinkResultmapList = []LinkResultMap{}

type QueryResult struct {
	selectVars []string
	Rows       []ResultMap
	Count      int
	Elapsed    time.Duration `msg:"-"`
}

func newQueryResult() QueryResult {
	return QueryResult{
		Rows: emptyResultMapList,
	}
}

func (qr QueryResult) Dump() {
	if len(qr.Rows) > 0 {
		for _, row := range qr.Rows {
			fmt.Println(row)
		}
		return
	}
	fmt.Println(qr.Count)
}

func (qr QueryResult) DumpToCSV(usePrefixes bool, db *HodDB, w io.Writer) error {
	csvwriter := csv.NewWriter(w)
	if len(qr.Rows) > 0 {
		for _, row := range qr.Rows {
			var line = make([]string, len(qr.selectVars))
			for idx, varname := range qr.selectVars {
				if usePrefixes {
					line[idx] = db.abbreviate(row[varname])
				} else {
					line[idx] = row[varname].String()
				}
			}
			if err := csvwriter.Write(line); err != nil {
				return err
			}
			csvwriter.Flush()
			if err := csvwriter.Error(); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

type ResultMap map[string]turtle.URI
type LinkResultMap map[turtle.URI]map[string]string

func (m LinkResultMap) MarshalJSON() ([]byte, error) {
	var n = make(map[string]map[string]string)
	for k, v := range m {
		n[k.String()] = v
	}
	return json.Marshal(n)
}

type ResultRow struct {
	row   []turtle.URI
	count int
}

func (rr ResultRow) Less(than btree.Item, ctx interface{}) bool {
	row := than.(*ResultRow)
	before := false
	for idx, item := range rr.row[:rr.count] {
		if before {
			return before
		}
		before = item.Value < row.row[idx].Value || item.Namespace < row.row[idx].Namespace
	}
	return before
}

var _emptyResultRow = make([]turtle.URI, 16)
var _RESULTROWPOOL = sync.Pool{
	New: func() interface{} {
		return &ResultRow{
			row:   make([]turtle.URI, 16),
			count: 0,
		}
	},
}

func getResultRow(num int) *ResultRow {
	r := _RESULTROWPOOL.Get().(*ResultRow)
	r.count = num
	return r
}

func finishResultRow(r *ResultRow) {
	r.count = 0
	_RESULTROWPOOL.Put(r)
}

func cleanResultRows(b *btree.BTree) {
	i := b.DeleteMax()
	for i != nil {
		row := i.(*ResultRow)
		finishResultRow(row)
		i = b.DeleteMax()
	}
}
