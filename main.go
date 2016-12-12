package main

import (
	"os"

	"github.com/op/go-logging"
	"github.com/urfave/cli"
)

// logger
var log *logging.Logger

func init() {
	log = logging.MustGetLogger("hod")
	var format = "%{color}%{level} %{shortfile} %{time:Jan 02 15:04:05} %{color:reset} ▶ %{message}"
	var logBackend = logging.NewLogBackend(os.Stderr, "", 0)
	logBackendLeveled := logging.AddModuleLevel(logBackend)
	logging.SetBackend(logBackendLeveled)
	logging.SetFormatter(logging.MustStringFormatter(format))
}

func main() {
	app := cli.NewApp()
	app.Name = "hod"
	app.Version = "0.1"
	app.Usage = "BRICK database and query engine"

	app.Commands = []cli.Command{
		{
			Name:   "benchload",
			Usage:  "Benchmark loading a turtle file",
			Action: benchLoad,
		},
		{
			Name:   "dump",
			Usage:  "Dump contents of turtle file",
			Action: dump,
		},
		{
			Name:   "viewclass",
			Usage:  "PDF visualization of class structure of file",
			Action: classGraph,
		},
		{
			Name:   "load",
			Usage:  "Load dataset into hoddb",
			Action: load,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "path, p",
					Value: ".",
					Usage: "Path where the database files will be placed",
				},
				cli.StringFlag{
					Name:  "frame, f",
					Value: "BrickFrame.ttl",
					Usage: "turtle file of brick relationships",
				},
				cli.StringFlag{
					Name:  "class, c",
					Value: "Brick.ttl",
					Usage: "turtle file of brick relationships",
				},
			},
		},
		{
			Name:   "start",
			Usage:  "Start hoddb from existing database",
			Action: start,
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "path, p",
					Value: ".",
					Usage: "Path where the database files will be placed",
				},
			},
		},
	}
	app.Run(os.Args)
}
