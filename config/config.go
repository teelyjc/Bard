package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type ChildNode struct {
	Token   string `json:"token"`
	Address string `json:"address"`
}

type Config struct {
	Configs struct {
		Master struct {
			Token          string `json:"token"`
			MonitorAddress string `json:"monitor_address"`
		} `json:"master"`
		Child struct {
			Nodes []ChildNode `json:"nodes"`
		} `json:"child"`
	} `json:"configs"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()
	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode: %w", err)
	}
	return &cfg, nil
}

func (c *Config) MasterToken() string {
	return c.Configs.Master.Token
}

func (c *Config) MasterMonitorAddress() string {
	if addr := c.Configs.Master.MonitorAddress; addr != "" {
		return addr
	}
	return "localhost:9090"
}

func (c *Config) ChildNodes() []ChildNode {
	return c.Configs.Child.Nodes
}

func (c *Config) ChildNode(index int) (*ChildNode, error) {
	nodes := c.Configs.Child.Nodes
	if len(nodes) == 0 {
		return nil, fmt.Errorf("config: no child nodes configured")
	}
	if index < 0 || index >= len(nodes) {
		return nil, fmt.Errorf("config: child index %d out of range (0–%d)", index, len(nodes)-1)
	}
	return &nodes[index], nil
}

