package ard

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type FederationConfig struct {
	MaxHops       int
	MaxRegistries int
}

type RegistryResults struct {
	Endpoint string          `json:"endpoint"`
	Response *SearchResponse `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func (client *Client) SearchFederated(ctx context.Context, registryBase string, request SearchRequest, config FederationConfig) ([]RegistryResults, error) {
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	if config.MaxHops < 0 || config.MaxRegistries < 0 {
		return nil, fmt.Errorf("ard: negative federation budget")
	}
	if config.MaxHops == 0 {
		config.MaxHops = 2
	}
	if config.MaxRegistries == 0 {
		config.MaxRegistries = 8
	}
	base, err := url.Parse(registryBase)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/search"
	base.RawPath, base.RawQuery, base.Fragment = "", "", ""
	type pendingRegistry struct {
		endpoint string
		depth    int
	}
	queue := []pendingRegistry{{endpoint: base.String()}}
	seen := map[string]bool{}
	var results []RegistryResults
	for len(queue) > 0 && len(results) < config.MaxRegistries {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		pending := queue[0]
		queue = queue[1:]
		parsed, err := url.Parse(pending.endpoint)
		if err != nil {
			return results, err
		}
		parsed.Fragment = ""
		parsed.Scheme, parsed.Host = strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Host)
		endpoint := parsed.String()
		if seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		current := request
		current.Federation = FederationReferrals
		if pending.depth > 0 {
			current.PageToken = ""
		}
		response, err := client.SearchEndpoint(ctx, endpoint, current)
		if err != nil {
			if pending.depth == 0 {
				return nil, err
			}
			results = append(results, RegistryResults{Endpoint: endpoint, Error: err.Error()})
			continue
		}
		results = append(results, RegistryResults{Endpoint: endpoint, Response: response})
		if pending.depth < config.MaxHops {
			for _, referral := range response.Referrals {
				if len(queue) >= config.MaxRegistries {
					break
				}
				queue = append(queue, pendingRegistry{endpoint: referral.URL, depth: pending.depth + 1})
			}
		}
	}
	return results, nil
}
