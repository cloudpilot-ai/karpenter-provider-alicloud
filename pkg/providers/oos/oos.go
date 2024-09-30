/*
Copyright 2024 The CloudPilot AI Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package oos

import (
	"context"
	"fmt"
	"sync"

	oos "github.com/alibabacloud-go/oos-20190601/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/patrickmn/go-cache"
)

type Provider interface {
	List(context.Context, string) (map[string]string, error)
}

type DefaultProvider struct {
	sync.Mutex
	cache  *cache.Cache
	oosapi *oos.Client
}

func NewDefaultProvider(oosapi *oos.Client, cache *cache.Cache) *DefaultProvider {
	return &DefaultProvider{
		oosapi: oosapi,
		cache:  cache,
	}
}

// List calls GetParametersByPath recursively with the provided input path.
// The result is a map of paths to values for those paths.
func (p *DefaultProvider) List(ctx context.Context, path string) (map[string]string, error) {
	p.Lock()
	defer p.Unlock()
	if paths, ok := p.cache.Get(path); ok {
		return paths.(map[string]string), nil
	}

	getParametersByPathRequest := &oos.GetParametersByPathRequest{
		Path:      tea.String(path),
		Recursive: tea.Bool(true),
	}
	runtime := &util.RuntimeOptions{}

	values := map[string]string{}
	for {
		result, err := p.oosapi.GetParametersByPathWithOptions(getParametersByPathRequest, runtime)
		if err != nil {
			return nil, err
		} else if result == nil || result.Body == nil {
			return nil, fmt.Errorf("get oos parameters for path: return null")
		}

		for _, parameter := range result.Body.Parameters {
			if parameter.Name == nil || parameter.Value == nil {
				continue
			}
			values[*parameter.Name] = *parameter.Value
		}
		getParametersByPathRequest.NextToken = result.Body.NextToken
		if getParametersByPathRequest.NextToken == nil || len(*getParametersByPathRequest.NextToken) == 0 {
			break
		}
	}

	p.cache.SetDefault(path, values)
	return values, nil
}
