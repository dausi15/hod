package config

import (
	"os"

	"github.com/op/go-logging"
	"github.com/spf13/viper"
)

type Config struct {
	DBPath        string
	BrickFrameTTL string
	BrickClassTTL string
	ReloadBrick   bool

	ShowNamespaces         bool
	ShowDependencyGraph    bool
	ShowQueryPlan          bool
	ShowQueryPlanLatencies bool
	ShowOperationLatencies bool
	ShowQueryLatencies     bool
	LogLevel               logging.Level

	ServerPort    string
	UseIPv6       bool
	ListenAddress string
	StaticPath    string
	TLSHost       string

	EnableCPUProfile   bool
	EnableMEMProfile   bool
	EnableBlockProfile bool
}

func init() {
	prefix := os.Getenv("GOPATH")
	// set defaults for config
	viper.SetDefault("DBPath", "_hoddb")
	viper.SetDefault("BrickFrameTTL", prefix+"/src/github.com/gtfierro/hod/BrickFrame.ttl")
	viper.SetDefault("BrickClassTTL", prefix+"/src/github.com/gtfierro/hod/Brick.ttl")
	viper.SetDefault("ReloadBrick", true)

	viper.SetDefault("ShowNamespaces", true)
	viper.SetDefault("ShowDependencyGraph", false)
	viper.SetDefault("ShowQueryPlan", false)
	viper.SetDefault("ShowQueryPlanLatencies", false)
	viper.SetDefault("ShowOperationLatencies", false)
	viper.SetDefault("ShowQueryLatencies", true)
	viper.SetDefault("LogLevel", "notice")

	viper.SetDefault("ServerPort", "47808")
	viper.SetDefault("UseIPv6", false)
	viper.SetDefault("ListenAddress", "127.0.0.1")
	viper.SetDefault("StaticPath", prefix+"/src/github.com/gtfierro/hod/server")
	viper.SetDefault("TLSHost", "") // disabled

	viper.SetDefault("EnableCPUProfile", false)
	viper.SetDefault("EnableMEMProfile", false)
	viper.SetDefault("EnableBlockProfile", false)

	viper.SetConfigName("hodconfig")
	// set search paths for config
	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/hoddb/")
	viper.AddConfigPath(prefix + "/src/github.com/gtfierro/hod")
}

func ReadConfig(file string) (*Config, error) {
	if len(file) > 0 {
		viper.SetConfigFile(file)
	}
	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}
	viper.SetEnvPrefix("HOD")
	viper.AutomaticEnv()

	level, err := logging.LogLevel(viper.GetString("LogLevel"))
	if err != nil {
		level = logging.DEBUG
	}

	c := &Config{
		DBPath:                 viper.GetString("DBPath"),
		BrickFrameTTL:          viper.GetString("BrickFrameTTL"),
		BrickClassTTL:          viper.GetString("BrickClassTTL"),
		ReloadBrick:            viper.GetBool("ReloadBrick"),
		ShowNamespaces:         viper.GetBool("ShowNamespaces"),
		ShowDependencyGraph:    viper.GetBool("ShowDependencyGraph"),
		ShowQueryPlan:          viper.GetBool("ShowQueryPlan"),
		ShowQueryPlanLatencies: viper.GetBool("ShowQueryPlanLatencies"),
		ShowOperationLatencies: viper.GetBool("ShowOperationLatencies"),
		ShowQueryLatencies:     viper.GetBool("ShowQueryLatencies"),
		LogLevel:               level,
		ServerPort:             viper.GetString("ServerPort"),
		UseIPv6:                viper.GetBool("UseIPv6"),
		ListenAddress:          viper.GetString("ListenAddress"),
		StaticPath:             viper.GetString("StaticPath"),
		TLSHost:                viper.GetString("TLSHost"),
		EnableCPUProfile:       viper.GetBool("EnableCPUProfile"),
		EnableMEMProfile:       viper.GetBool("EnableMEMProfile"),
		EnableBlockProfile:     viper.GetBool("EnableBlockProfile"),
	}
	return c, nil
}
