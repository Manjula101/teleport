// Teleport
// Copyright (C) 2025 Gravitational, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package inventoryv1

import (
	"context"

	"github.com/gravitational/trace"

	inventoryv1 "github.com/gravitational/teleport/api/gen/proto/go/teleport/inventory/v1"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/lib/authz"
	"github.com/gravitational/teleport/lib/cache"
)

type ServiceConfig struct {
	// Authorizer is the authorizer used to check access to resources.
	Authorizer authz.Authorizer
	// InventoryCache is the inventory cache containing instances.
	InventoryCache *cache.InventoryCache
}

func (c *ServiceConfig) CheckAndSetDefaults() error {
	if c.Authorizer == nil {
		return trace.BadParameter("missing Authorizer")
	}
	if c.InventoryCache == nil {
		return trace.BadParameter("missing InventoryCache")
	}
	return nil
}

// Service implements the teleport.inventory.v1.InventoryService RPC service.
type Service struct {
	inventoryv1.UnimplementedInventoryServiceServer

	authorizer     authz.Authorizer
	inventoryCache *cache.InventoryCache
}

// NewService returns a new inventory service.
func NewService(cfg ServiceConfig) (*Service, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	return &Service{
		authorizer:     cfg.Authorizer,
		inventoryCache: cfg.InventoryCache,
	}, nil
}

// ListUnifiedInstances returns a page of teleport instances and bot_instances. This API will refuse any requests when the cache is unhealthy or not yet
// fully initialized.
func (s *Service) ListUnifiedInstances(ctx context.Context, req *inventoryv1.ListUnifiedInstancesRequest) (*inventoryv1.ListUnifiedInstancesResponse, error) {
	authCtx, err := s.authorizer.Authorize(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// If no kinds are specified, default to all kinds.
	if req.Filter == nil {
		req.Filter = &inventoryv1.ListUnifiedInstancesFilter{}
		req.Filter.InstanceTypes = []string{types.KindInstance, types.KindBotInstance}
	} else if len(req.Filter.InstanceTypes) == 0 {
		req.Filter.InstanceTypes = []string{types.KindInstance, types.KindBotInstance}
	} else {
		req.Filter.InstanceTypes = utils.Deduplicate(req.Filter.InstanceTypes)
	}

	// Ensure that the kinds requested align with the user's permissions.
	for _, kind := range req.Filter.InstanceTypes {
		switch kind {
		case types.KindInstance:
			if err := authCtx.CheckAccessToKind(types.KindInstance, types.VerbList, types.VerbRead); err != nil {
				return nil, trace.Wrap(err)
			}
			continue
		case types.KindBotInstance:
			if err := authCtx.CheckAccessToKind(types.KindBotInstance, types.VerbList, types.VerbRead); err != nil {
				return nil, trace.Wrap(err)
			}
			continue
		}
	}

	resp, err := s.inventoryCache.ListUnifiedInstances(ctx, req)
	return resp, trace.Wrap(err)
}
