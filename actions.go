package main

import (
	"fmt"
	hod "github.com/gtfierro/hod/db"
	"github.com/gtfierro/hod/goraptor"
	query "github.com/gtfierro/hod/query"
	"os/user"
	"strings"

	"github.com/chzyer/readline"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

func benchLoad(c *cli.Context) error {
	if c.NArg() == 0 {
		return errors.New("Need to specify a turtle file to load")
	}
	filename := c.Args().Get(0)
	p := turtle.GetParser()
	ds, duration := p.Parse(filename)
	rate := float64((float64(ds.NumTriples()) / float64(duration.Nanoseconds())) * 1e9)
	fmt.Printf("Loaded %d triples, %d namespaces in %s (%.0f/sec)\n", ds.NumTriples(), ds.NumNamespaces(), duration, rate)
	return nil
}

func load(c *cli.Context) error {
	if c.NArg() == 0 {
		return errors.New("Need to specify a turtle file to load")
	}
	filename := c.Args().Get(0)
	path := c.String("path")
	p := turtle.GetParser()
	ds, duration := p.Parse(filename)
	rate := float64((float64(ds.NumTriples()) / float64(duration.Nanoseconds())) * 1e9)
	log.Infof("Loaded %d triples, %d namespaces in %s (%.0f/sec)", ds.NumTriples(), ds.NumNamespaces(), duration, rate)

	frame := c.String("frame")
	relships, _ := p.Parse(frame)

	db, err := hod.NewDB(path)
	if err != nil {
		return err
	}
	err = db.LoadRelationships(relships)
	err = db.LoadDataset(ds)
	if err != nil {
		return err
	}
	currentUser, err := user.Current()
	if err != nil {
		return err
	}
	fmt.Println("Successfully loaded dataset!")
	bufQuery := ""
	rl, err := readline.NewEx(&readline.Config{
		Prompt:                 "(hod)> ",
		HistoryFile:            currentUser.HomeDir + "/.hod-query-history",
		DisableAutoSaveHistory: true,
	})
	for {
		line, err := rl.Readline()
		if err != nil {
			break
		}
		if len(line) == 0 {
			continue
		}
		bufQuery += line + " "
		if !strings.HasSuffix(line, ";") {
			rl.SetPrompt(">>> ...")
			continue
		}
		rl.SetPrompt("(hod)> ")
		rl.SaveHistory(bufQuery)
		q, err := query.Parse(strings.NewReader(bufQuery))
		if err != nil {
			log.Error(err)
		} else {
			db.RunQuery(q)
		}
		bufQuery = ""
	}

	return nil
}

func dump(c *cli.Context) error {
	if c.NArg() == 0 {
		return errors.New("Need to specify a turtle file to load")
	}
	filename := c.Args().Get(0)
	p := turtle.GetParser()
	ds, _ := p.Parse(filename)
	for _, triple := range ds.Triples {
		var s = triple.Subject.Value
		var p = triple.Predicate.Value
		var o = triple.Object.Value
		for pfx, full := range ds.Namespaces {
			if triple.Subject.Namespace == full {
				s = pfx + ":" + s
			}
			if triple.Predicate.Namespace == full {
				p = pfx + ":" + p
			}
			if triple.Object.Namespace == full {
				o = pfx + ":" + o
			}
		}
		fmt.Printf("%s\t%s\t%s\n", s, p, o)
	}
	return nil
}
