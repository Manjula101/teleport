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

// TODO(rudream): move this to lib/cache/inventory
package cache

import (
	"context"
	"encoding/base64"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gravitational/trace"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"
	"rsc.io/ordered"

	"github.com/gravitational/teleport/api/defaults"
	inventoryv1 "github.com/gravitational/teleport/api/gen/proto/go/teleport/inventory/v1"
	machineidv1 "github.com/gravitational/teleport/api/gen/proto/go/teleport/machineid/v1"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/lib/services"
)

const (
	// instancePrefix is the backend prefix for teleport instances.
	instancePrefix = "instances"

	// botInstancePrefix is the backend prefix for bot instances.
	botInstancePrefix = "bot_instance"
)

type inventoryIndex string

const (
	// inventoryAlphabeticalIndex sorts instances by display name (bot name
	// or instance hostname), unique ID (bot instance ID or instance host ID)
	// and type ("bot" or "instance").
	inventoryAlphabeticalIndex inventoryIndex = "alphabetical"

	// inventoryTypeIndex sorts instances by type, display name
	// and unique ID.
	inventoryTypeIndex inventoryIndex = "type"

	// inventoryIDIndex allows lookup by instance ID.
	// For instances: <instance id>
	// For bot instances: <bot name>/<instance id> (composite key for uniqueness)
	inventoryIDIndex inventoryIndex = "id"
)

// inventoryInstance is a wrapper for either a teleport instance or a bot instance.
type inventoryInstance struct {
	instance *types.InstanceV1
	bot      *machineidv1.BotInstance
}

// isInstance returns true if this wrapper contains a teleport instance (not a bot instance).
func (u *inventoryInstance) isInstance() bool {
	return u.instance != nil
}

// getInstanceID returns a unique ID for this instance.
// For instances, this is the instance ID. For bot names, this is `<bot name/><instance id>`.
func (u *inventoryInstance) getInstanceID() string {
	if u.isInstance() {
		return u.instance.GetName()
	}
	botName := u.bot.GetSpec().GetBotName()
	instanceID := u.bot.GetSpec().GetInstanceId()
	// Fallback to metadata name if instance ID is not set
	if instanceID == "" {
		instanceID = u.bot.GetMetadata().GetName()
	}
	return botName + "/" + instanceID
}

// getKind returns the resource kind for this instance.
func (u *inventoryInstance) getKind() string {
	if u.isInstance() {
		return types.KindInstance
	}
	return types.KindBotInstance
}

// getAlphabeticalKey returns the composite key for alphabetical sorting.
func (u *inventoryInstance) getAlphabeticalKey() string {
	var name, id string
	if u.isInstance() {
		name = u.instance.GetHostname()
		id = u.instance.GetName()
	} else {
		name = u.bot.GetSpec().GetBotName()
		id = u.bot.GetSpec().GetInstanceId()
		// Fallback to metadata name if instance ID is not set
		if id == "" {
			id = u.bot.GetMetadata().GetName()
		}
	}

	return string(ordered.Encode(name, id, u.getKind()))
}

// getTypeKey returns the composite key for sorting by type.
func (u *inventoryInstance) getTypeKey() string {
	var name, id string
	if u.isInstance() {
		name = u.instance.GetHostname()
		id = u.instance.GetName()
	} else {
		name = u.bot.GetSpec().GetBotName()
		id = u.bot.GetSpec().GetInstanceId()
		// Fallback to metadata name if instance ID is not set
		if id == "" {
			id = u.bot.GetMetadata().GetName()
		}
	}

	return string(ordered.Encode(u.getKind(), name, id))
}

// getIDKey returns the key for lookup by instance ID.
// We use ordered encoding for safe lexicographic ordering
func (u *inventoryInstance) getIDKey() string {
	return string(ordered.Encode(u.getInstanceID()))
}

// clone returns a deep copy of the unified instance.
func (u *inventoryInstance) clone() *inventoryInstance {
	if u.isInstance() {
		return &inventoryInstance{instance: utils.CloneProtoMsg(u.instance)}
	}
	return &inventoryInstance{bot: proto.CloneOf(u.bot)}
}

// InventoryCacheConfig holds the configuration parameters for the InventoryCache.
type InventoryCacheConfig struct {
	// PrimaryCache is Teleport's primary cache.
	PrimaryCache *Cache

	// Events is the events service for watching backend events.
	Events types.Events

	// Inventory is the inventory service.
	Inventory services.Inventory

	// BotInstanceCache is the service for reading bot instances.
	BotInstanceCache services.BotInstance

	// TargetVersion is the target Teleport version for the cluster.
	TargetVersion string

	Logger *slog.Logger
}

func (c *InventoryCacheConfig) CheckAndSetDefaults() error {
	if c.PrimaryCache == nil {
		return trace.BadParameter("missing PrimaryCache")
	}
	if c.Events == nil {
		return trace.BadParameter("missing Events")
	}
	if c.Inventory == nil {
		return trace.BadParameter("missing Inventory")
	}
	if c.BotInstanceCache == nil {
		return trace.BadParameter("missing BotInstanceCache")
	}

	if c.Logger == nil {
		c.Logger = slog.Default()
	}

	return nil
}

// InventoryCache is the cache for teleport and bot instances.
type InventoryCache struct {
	mu sync.RWMutex
	// healthy is whether the cache is healthy and ready to serve requests.
	healthy atomic.Bool
	// initDone is a channel used to ensure clean shutdowns.
	initDone chan struct{}

	cfg InventoryCacheConfig

	ctx    context.Context
	cancel context.CancelFunc

	// store is the unified sortcache that holds both teleport and bot instances.
	store *store[*inventoryInstance, inventoryIndex]
}

func NewInventoryCache(cfg InventoryCacheConfig) (*InventoryCache, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ic := &InventoryCache{
		cfg: cfg,

		// Create the sortcache
		store: newStore(
			"unified_instance",
			func(u *inventoryInstance) *inventoryInstance {
				return u.clone()
			},
			map[inventoryIndex]func(*inventoryInstance) string{
				inventoryAlphabeticalIndex: func(u *inventoryInstance) string {
					return u.getAlphabeticalKey()
				},
				inventoryTypeIndex: func(u *inventoryInstance) string {
					return u.getTypeKey()
				},
				inventoryIDIndex: func(u *inventoryInstance) string {
					return u.getIDKey()
				},
			},
		),

		// Create a channel that will close when the initialization is done.
		initDone: make(chan struct{}),

		ctx:    ctx,
		cancel: cancel,
	}

	go ic.initializeAndWatchWithRetry(ctx)

	return ic, nil
}

// IsHealthy returns true if the cache is healthy and initialized.
func (ic *InventoryCache) IsHealthy() bool {
	return ic.healthy.Load()
}

func (ic *InventoryCache) Close() error {
	ic.cancel()
	// Wait for initDone channel to finish so we can close gracefully.
	<-ic.initDone
	return nil
}

// calculateReadsPerSecond calculates the rate limit to use for backend reads based on cluster size.
// With this implementation, these are some of the expected rate limits and corresponding total times based on cluster size:
//
// Cluster size | Reads per second | Total time to finish all reads
// -------------|------------------|-------------------------------
// 500          | 283              | 1.77s
// 1,000        | 298              | 3.36s
// 2,000        | 322              | 6.21s
// 4,000        | 363              | 11.02s
// 8,000        | 433              | 18.47s
// 32,000       | 789              | 40.56s
// 64,000       | 1219             | 52.5s
// 128,000      | 2035             | 1m03s
// 256,000      | 3605             | 1m11s
func calculateReadsPerSecond(clusterSize int) int {
	// minimumComponent is the minimum value of reads per second we never want to drop below.
	const minimumComponent = 256

	// linearComponent ensures we stay under a worst-case upper bound init time of 90s.
	linearComponent := clusterSize / 90

	// subLinearComponent ensures that growth is sub-linear across most reasonable cluster sizes.
	subLinearComponent := int(math.Sqrt(float64(clusterSize)))

	return minimumComponent + linearComponent + subLinearComponent
}

// initializeAndWatchWithRetry runs initializeAndWatch with a retry every 10 seconds if it fails.
func (ic *InventoryCache) initializeAndWatchWithRetry(ctx context.Context) {
	defer close(ic.initDone)

	const retryInterval = 10 * time.Second

	for {
		ic.cfg.Logger.DebugContext(ctx, "Attempting to initialize inventory cache")

		// Attempt to initialize and watch
		if err := ic.initializeAndWatch(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}

			ic.cfg.Logger.WarnContext(ctx, "Failed to initialize inventory cache, retrying in 10 seconds",
				"error", err)

			// Wait before retrying
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryInterval):
			}
			continue
		}

		return
	}
}

// initializeAndWatch initializes the inventory cache and begins watching for instance and bot_instance backend events.
func (ic *InventoryCache) initializeAndWatch(ctx context.Context) error {
	// Wait for primary cache to be ready.
	if err := ic.waitForPrimaryCacheInit(ctx); err != nil {
		return trace.Wrap(err, "Failed to wait for primary cache init")
	}

	// Setup the backend watcher.
	watcher, err := ic.setupWatcher(ctx)
	if err != nil {
		return trace.Wrap(err, "Failed to set up backend watcher")
	}
	defer watcher.Close()

	// Wait for the watcher to be ready.
	if err := ic.waitForWatcherInit(ctx, watcher); err != nil {
		return trace.Wrap(err, "Failed to wait for watcher init")
	}

	// Calculate the rate limit to use.
	primaryCacheSize := ic.cfg.PrimaryCache.GetCacheSize()
	readsPerSecond := calculateReadsPerSecond(primaryCacheSize)

	// Populate the cache with teleport instance and bot instances.
	if err := ic.populateCache(ctx, readsPerSecond); err != nil {
		return trace.Wrap(err, "failed to populate inventory cache")
	}

	// Mark cache as healthy.
	ic.healthy.Store(true)

	// This runs infinitely until the context is cancelled, so the return below won't be hit until shutdown.
	ic.processEvents(ctx, watcher)

	return nil
}

// waitForPrimaryCacheInit waits for the primary cache to be initialized.
func (ic *InventoryCache) waitForPrimaryCacheInit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return trace.Wrap(ctx.Err())
	case <-ic.cfg.PrimaryCache.FirstTimeInit():
		return nil
	}
}

// setupWatcher sets up a watcher for instance and bot_instance events.
func (ic *InventoryCache) setupWatcher(ctx context.Context) (types.Watcher, error) {
	watcher, err := ic.cfg.Events.NewWatcher(ctx, types.Watch{
		Name: "inventory_cache",
		Kinds: []types.WatchKind{
			{Kind: types.KindInstance},
			{Kind: types.KindBotInstance},
		},
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return watcher, nil
}

// waitForWatcherInit waits for the watcher to finish initializing.
func (ic *InventoryCache) waitForWatcherInit(ctx context.Context, watcher types.Watcher) error {
	select {
	case <-ctx.Done():
		// Context was cancelled
		return trace.Wrap(ctx.Err())

	case event := <-watcher.Events():
		if event.Type != types.OpInit {
			return trace.BadParameter("expected OpInit event, got %v", event.Type)
		}
		return nil
	}
}

// populateCache reads teleport and bot instances and populates the cache with rate limiting.
func (ic *InventoryCache) populateCache(ctx context.Context, readsPerSecond int) error {
	limiter := rate.NewLimiter(rate.Limit(readsPerSecond), readsPerSecond)

	if err := ic.populateInstances(ctx, limiter); err != nil {
		return trace.Wrap(err)
	}

	if err := ic.populateBotInstances(ctx, limiter); err != nil {
		return trace.Wrap(err)
	}

	return nil
}

// populateInstances reads teleport instances from the inventory service with rate limiting.
func (ic *InventoryCache) populateInstances(ctx context.Context, limiter *rate.Limiter) error {
	instanceStream := ic.cfg.Inventory.GetInstances(ctx, types.InstanceFilter{})

	for instanceStream.Next() {
		if err := limiter.Wait(ctx); err != nil {
			return trace.Wrap(err)
		}

		instance := instanceStream.Item()

		instanceV1, ok := instance.(*types.InstanceV1)
		if !ok {
			ic.cfg.Logger.WarnContext(ctx, "Instance is not InstanceV1", "instance", instance.GetName())
			continue
		}

		// Add it to the cache
		ui := &inventoryInstance{instance: instanceV1}
		if err := ic.store.put(ui); err != nil {
			ic.cfg.Logger.WarnContext(ctx, "Failed to add instance to cache", "instance", instanceV1.GetName(), "error", err)
			continue
		}
	}

	return trace.Wrap(instanceStream.Done())
}

// populateBotInstances reads bot instances from the bot instance service with rate limiting.
func (ic *InventoryCache) populateBotInstances(ctx context.Context, limiter *rate.Limiter) error {
	var pageToken string

	for {
		if err := limiter.Wait(ctx); err != nil {
			return trace.Wrap(err)
		}

		botInstances, nextToken, err := ic.cfg.BotInstanceCache.ListBotInstances(
			ctx,
			defaults.DefaultChunkSize,
			pageToken,
			nil,
		)
		if err != nil {
			return trace.Wrap(err)
		}

		for _, botInstance := range botInstances {
			// Add it to the cache
			ui := &inventoryInstance{bot: botInstance}
			if err := ic.store.put(ui); err != nil {
				ic.cfg.Logger.WarnContext(ctx, "Failed to add bot instance to cache", "bot_instance", botInstance.Metadata.Name, "error", err)
				continue
			}
		}

		if nextToken == "" || len(botInstances) == 0 {
			break
		}

		pageToken = nextToken
	}

	return nil
}

// processEvents processes events from the watcher.
func (ic *InventoryCache) processEvents(ctx context.Context, watcher types.Watcher) {
	for {
		select {
		case <-ctx.Done():
			return

		case event := <-watcher.Events():
			if err := ic.processEvent(event); err != nil {
				ic.cfg.Logger.WarnContext(ctx, "Failed to process event", "error", err)
			}
		}
	}
}

// processEvent processes an event from the watcher.
func (ic *InventoryCache) processEvent(event types.Event) error {
	switch event.Type {
	case types.OpPut:
		return ic.processPutEvent(event)
	case types.OpDelete:
		return ic.processDeleteEvent(event)
	default:
		// Unknown event type
		return nil
	}
}

// processPutEvent processes an OpPut event.
func (ic *InventoryCache) processPutEvent(event types.Event) error {
	switch resource := event.Resource.(type) {
	case *types.InstanceV1:
		// Add/update it in the cache
		ui := &inventoryInstance{instance: resource}
		if err := ic.store.put(ui); err != nil {
			return trace.Wrap(err)
		}
	case types.Resource153UnwrapperT[*machineidv1.BotInstance]:
		// Handle bot instances wrapped in Resource153ToLegacy adapter
		botInstance := resource.UnwrapT()
		ui := &inventoryInstance{bot: botInstance}
		if err := ic.store.put(ui); err != nil {
			return trace.Wrap(err)
		}
	}

	return nil
}

// processDeleteEvent handles OpDelete events.
func (ic *InventoryCache) processDeleteEvent(event types.Event) error {
	// For delete events, the EventsService returns a ResourceHeader
	switch resource := event.Resource.(type) {
	case *types.InstanceV1:
		// Find and remove the instance from the cache.
		instanceID := resource.GetName()
		encodedID := string(ordered.Encode(instanceID))
		if existing, err := ic.store.get(inventoryIDIndex, encodedID); err == nil {
			ic.store.delete(existing)
		}
	case *types.ResourceHeader:
		// For regular instances, use the instance ID directly
		instanceID := resource.GetName()
		encodedID := string(ordered.Encode(instanceID))
		if existing, err := ic.store.get(inventoryIDIndex, encodedID); err == nil {
			ic.store.delete(existing)
		}
	case types.Resource153UnwrapperT[*machineidv1.BotInstance]:
		botInstance := resource.UnwrapT()
		botName := botInstance.GetSpec().GetBotName()
		instanceID := botInstance.GetSpec().GetInstanceId()
		// Fallback to metadata name if instance ID is not set
		if instanceID == "" {
			instanceID = botInstance.GetMetadata().GetName()
		}
		key := botName + "/" + instanceID
		encodedID := string(ordered.Encode(key))
		if existing, err := ic.store.get(inventoryIDIndex, encodedID); err == nil {
			ic.store.delete(existing)
		}
	}

	return nil
}

// ListUnifiedInstances returns a page of instances and bot_instances. This API will refuse any requests when the cache is unhealthy or not yet
// fully initialized.
func (ic *InventoryCache) ListUnifiedInstances(ctx context.Context, req *inventoryv1.ListUnifiedInstancesRequest) (*inventoryv1.ListUnifiedInstancesResponse, error) {
	if !ic.IsHealthy() {
		return nil, trace.ConnectionProblem(nil, "inventory cache is not yet healthy")
	}

	if req.PageSize <= 0 {
		req.PageSize = defaults.DefaultChunkSize
	}

	// Decode the PageToken from base64
	var startKey string
	if req.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.PageToken)
		if err != nil {
			return nil, trace.BadParameter("invalid page token: %v", err)
		}
		startKey = string(decoded)
	} else {
		// If no kinds filter is specified or multiple kinds are, start from the beginning.
		// If we're only filtering for 1 kind, use type index with kind prefix.
		if req.Filter != nil && len(req.Filter.InstanceTypes) == 1 {
			kind := req.Filter.InstanceTypes[0]
			startKey = string(ordered.Encode(kind))
		}
	}

	var items []*inventoryv1.UnifiedInstanceItem
	var nextPageToken string

	index := inventoryAlphabeticalIndex
	var endKey string
	// Determine if we should use the type index.
	useTypeIndex := req.Filter != nil && len(req.Filter.InstanceTypes) == 1
	if useTypeIndex {
		index = inventoryTypeIndex
		if req.PageToken == "" {
			kind := req.Filter.InstanceTypes[0]
			// Use \xff to create an upper bound for this kind prefix
			endKey = string(ordered.Encode(kind)) + "\xff"
		}
	}

	for sf := range ic.store.cache.Ascend(index, startKey, endKey) {
		if !ic.matchesFilter(sf, req.Filter) {
			continue
		}

		if len(items) == int(req.PageSize) {
			var rawKey string
			if index == inventoryAlphabeticalIndex {
				rawKey = sf.getAlphabeticalKey()
			} else {
				rawKey = sf.getTypeKey()
			}
			// Encode the next page token to base64
			nextPageToken = base64.StdEncoding.EncodeToString([]byte(rawKey))
			break
		}

		item := ic.unifiedInstanceToProto(sf)
		items = append(items, item)
	}

	return &inventoryv1.ListUnifiedInstancesResponse{
		Items:         items,
		NextPageToken: nextPageToken,
	}, nil
}

// matchesFilter checks if a unified instance matches the filter criteria.
func (ic *InventoryCache) matchesFilter(_ *inventoryInstance, _ *inventoryv1.ListUnifiedInstancesFilter) bool {
	// TODO(rudream): implement filtering for listing instances.
	return true
}

// unifiedInstanceToProto converts a unified instance to a proto UnifiedInstanceItem.
func (ic *InventoryCache) unifiedInstanceToProto(ui *inventoryInstance) *inventoryv1.UnifiedInstanceItem {
	if ui.isInstance() {
		return &inventoryv1.UnifiedInstanceItem{
			Item: &inventoryv1.UnifiedInstanceItem_Instance{
				Instance: ui.instance,
			},
		}
	}
	return &inventoryv1.UnifiedInstanceItem{
		Item: &inventoryv1.UnifiedInstanceItem_BotInstance{
			BotInstance: ui.bot,
		},
	}
}
