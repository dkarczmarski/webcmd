package server

import (
	"github.com/dkarczmarski/webcmd/pkg/config"
)

type RequestResolver struct {
	commands map[string]*config.URLCommand // "METHOD path" -> command
	apiKeys  map[string]string             // api key -> auth name
}

func NewRequestResolver(cfg *config.Config) *RequestResolver {
	resolver := &RequestResolver{
		commands: make(map[string]*config.URLCommand),
		apiKeys:  make(map[string]string),
	}

	for i := range cfg.URLCommands {
		cmd := &cfg.URLCommands[i]
		resolver.commands[cmd.URL] = cmd
	}

	for _, auth := range cfg.Authorization {
		resolver.apiKeys[auth.Key] = auth.Name
	}

	return resolver
}

func (r *RequestResolver) ResolveURLCommand(requestURL string) (*config.URLCommand, bool) {
	cmd, ok := r.commands[requestURL]

	return cmd, ok
}

func (r *RequestResolver) ResolveAuthName(apiKey string) (string, bool) {
	name, ok := r.apiKeys[apiKey]

	return name, ok
}
