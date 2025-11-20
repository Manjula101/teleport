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

package cache

import (
	"context"
	"testing"
	"time"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/backend/memory"
	"github.com/gravitational/teleport/lib/services/local"
)

// TestConfig holds the resources needed for creating a test cache
type TestConfig struct {
	Backend *backend.Wrapper
	Cache   *Cache
	EventsC chan Event
}

func (tc *TestConfig) Close() error {
	var errors []error
	if tc.Cache != nil {
		errors = append(errors, tc.Cache.Close())
	}
	if tc.Backend != nil {
		errors = append(errors, tc.Backend.Close())
	}
	return trace.NewAggregate(errors...)
}

// SetupTestCache creates a new test cache
func SetupTestCache(t *testing.T, setupConfig SetupConfigFn) (*TestConfig, error) {
	ctx := context.Background()

	bk, err := memory.New(memory.Config{
		Context: ctx,
		Mirror:  true,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	bkWrapper := backend.NewWrapper(bk)

	eventsC := make(chan Event, 1024)

	clusterConfig, err := local.NewClusterConfigurationService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	idService, err := local.NewTestIdentityService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	dynamicWindowsDesktopService, err := local.NewDynamicWindowsDesktopService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	trustS := local.NewCAService(bkWrapper)
	provisionerS := local.NewProvisioningService(bkWrapper)
	eventsS := local.NewEventsService(bkWrapper)
	presenceS := local.NewPresenceService(bkWrapper)
	accessS := local.NewAccessService(bkWrapper)
	dynamicAccessS := local.NewDynamicAccessService(bkWrapper)
	restrictions := local.NewRestrictionsService(bkWrapper)
	apps := local.NewAppService(bkWrapper)
	kubernetes := local.NewKubernetesService(bkWrapper)
	databases := local.NewDatabasesService(bkWrapper)
	databaseServices := local.NewDatabaseServicesService(bkWrapper)
	windowsDesktops := local.NewWindowsDesktopService(bkWrapper)

	samlIDPServiceProviders, err := local.NewSAMLIdPServiceProviderService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	userGroups, err := local.NewUserGroupService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	oktaSvc, err := local.NewOktaService(bkWrapper, bkWrapper.Clock())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	igSvc, err := local.NewIntegrationsService(bkWrapper, local.WithIntegrationsServiceCacheMode(true))
	if err != nil {
		return nil, trace.Wrap(err)
	}

	userTasksSvc, err := local.NewUserTasksService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	dcSvc, err := local.NewDiscoveryConfigService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	ulsSvc, err := local.NewUserLoginStateService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	secReportsSvc, err := local.NewSecReportsService(bkWrapper, bkWrapper.Clock())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	accessListsSvc, err := local.NewAccessListService(bkWrapper, bkWrapper.Clock())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	accessMonitoringRuleService, err := local.NewAccessMonitoringRulesService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	crownJewelsSvc, err := local.NewCrownJewelsService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	spiffeFederationsSvc, err := local.NewSPIFFEFederationService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	workloadIdentitySvc, err := local.NewWorkloadIdentityService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	databaseObjectsSvc, err := local.NewDatabaseObjectService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	kubeWaitingContSvc, err := local.NewKubeWaitingContainerService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	notificationsSvc, err := local.NewNotificationsService(bkWrapper, bkWrapper.Clock())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	staticHostUserService, err := local.NewStaticHostUserService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	autoUpdateService, err := local.NewAutoUpdateService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	provisioningStates, err := local.NewProvisioningStateService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	identityCenter, err := local.NewIdentityCenterService(local.IdentityCenterServiceConfig{
		Backend: bkWrapper,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	pluginStaticCredentials, err := local.NewPluginStaticCredentialsService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	gitServers, err := local.NewGitServerService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	healthCheckConfig, err := local.NewHealthCheckConfigService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	botInstanceService, err := local.NewBotInstanceService(bkWrapper, bkWrapper.Clock())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	recordingEncryption, err := local.NewRecordingEncryptionService(bkWrapper)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	plugin := local.NewPluginsService(bkWrapper)

	cache, err := New(setupConfig(Config{
		Context:                 ctx,
		Events:                  eventsS,
		ClusterConfig:           clusterConfig,
		Provisioner:             provisionerS,
		Trust:                   trustS,
		Users:                   idService,
		Access:                  accessS,
		DynamicAccess:           dynamicAccessS,
		Presence:                presenceS,
		AppSession:              idService,
		WebSession:              idService.WebSessions(),
		WebToken:                idService,
		SnowflakeSession:        idService,
		Restrictions:            restrictions,
		Apps:                    apps,
		Kubernetes:              kubernetes,
		DatabaseServices:        databaseServices,
		Databases:               databases,
		WindowsDesktops:         windowsDesktops,
		DynamicWindowsDesktops:  dynamicWindowsDesktopService,
		SAMLIdPServiceProviders: samlIDPServiceProviders,
		UserGroups:              userGroups,
		Okta:                    oktaSvc,
		Integrations:            igSvc,
		UserTasks:               userTasksSvc,
		DiscoveryConfigs:        dcSvc,
		UserLoginStates:         ulsSvc,
		SecReports:              secReportsSvc,
		AccessLists:             accessListsSvc,
		KubeWaitingContainers:   kubeWaitingContSvc,
		Notifications:           notificationsSvc,
		AccessMonitoringRules:   accessMonitoringRuleService,
		CrownJewels:             crownJewelsSvc,
		SPIFFEFederations:       spiffeFederationsSvc,
		DatabaseObjects:         databaseObjectsSvc,
		StaticHostUsers:         staticHostUserService,
		AutoUpdateService:       autoUpdateService,
		ProvisioningStates:      provisioningStates,
		IdentityCenter:          identityCenter,
		PluginStaticCredentials: pluginStaticCredentials,
		GitServers:              gitServers,
		HealthCheckConfig:       healthCheckConfig,
		WorkloadIdentity:        workloadIdentitySvc,
		BotInstanceService:      botInstanceService,
		RecordingEncryption:     recordingEncryption,
		Plugin:                  plugin,
		MaxRetryPeriod:          200 * time.Millisecond,
		EventsC:                 eventsC,
	}))
	if err != nil {
		return nil, trace.Wrap(err)
	}

	select {
	case event := <-eventsC:
		if event.Type != WatcherStarted {
			return nil, trace.CompareFailed("%q != %q %s", event.Type, WatcherStarted, event)
		}
	case <-time.After(time.Second):
		return nil, trace.ConnectionProblem(nil, "wait for the watcher to start")
	}

	return &TestConfig{
		Backend: bkWrapper,
		Cache:   cache,
		EventsC: eventsC,
	}, nil
}
