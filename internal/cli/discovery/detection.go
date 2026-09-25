package discovery

import (
	"github.com/agentapprovalprotocol/agentapprovalprotocol/adapters"
	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
	"github.com/withHumanAI/withHuman/internal/cli/installation"
)

func (e Env) Manager() (*adapters.Manager, error) {
	return installation.New(e.ConfigDir, adapters.Environment{Home: e.Home, Executable: e.Binary, LookPath: e.lookPath, Run: e.run, Getenv: e.getenv})
}

func detectRuntime(env Env, key agentruntime.Key) (Detection, error) {
	manager, err := env.Manager()
	if err != nil {
		return Detection{}, err
	}
	adapter, err := manager.Lookup(string(key))
	if err != nil {
		return Detection{}, err
	}
	d, err := adapter.Detect()
	return Detection{Runtime: d.Runtime, Installed: d.Installed, BinaryPath: d.BinaryPath, Version: d.Version, ConfigPath: d.ConfigPath, AlreadyHooked: d.AlreadyHooked}, err
}
func (c claudeCode) Detect(env Env) (Detection, error) {
	d, err := detectRuntime(env, c.Key())
	if err != nil || !d.Installed {
		return d, err
	}
	servers, err := c.servers(env)
	d.MCPServers = detectionServers(servers)
	return d, err
}
func (c codex) Detect(env Env) (Detection, error) {
	d, err := detectRuntime(env, c.Key())
	if err != nil || !d.Installed {
		return d, err
	}
	table, err := c.load(env)
	if err != nil {
		return d, err
	}
	d.MCPServers = detectionServers(c.servers(env, table, d.ConfigPath))
	return d, nil
}
func (h hermes) Detect(env Env) (Detection, error) {
	d, err := detectRuntime(env, h.Key())
	if err != nil || !d.Installed {
		return d, err
	}
	root, err := h.load(env)
	if err != nil {
		return d, err
	}
	d.MCPServers = detectionServers(h.servers(env, root, d.ConfigPath))
	return d, nil
}
func (o openClaw) Detect(env Env) (Detection, error) {
	d, err := detectRuntime(env, o.Key())
	if err != nil || !d.Installed {
		return d, err
	}
	raw, existed, err := configfile.ReadFile(o.configPath(env))
	if err != nil {
		return d, err
	}
	if doc, err := configfile.LoadJSONObject(raw, existed); err == nil {
		if obj, ok := doc.Root.ObjectIfPresent("mcpServers"); ok {
			d.MCPServers = detectionServers(env.ownServers(claudeStyleServers(obj, d.ConfigPath, env.Cwd)))
		}
	}
	return d, nil
}
func (d deepSeek) Detect(env Env) (Detection, error) {
	det, err := detectRuntime(env, d.Key())
	if err != nil || !det.Installed {
		return det, err
	}
	servers, err := d.servers(env)
	det.MCPServers = detectionServers(servers)
	return det, err
}
func (p pi) Detect(env Env) (Detection, error) { return detectRuntime(env, p.Key()) }
