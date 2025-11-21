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

package expression

import (
	"github.com/gravitational/teleport/lib/expression"
	"github.com/gravitational/teleport/lib/utils/typical"
)

func NewBotInstanceExpressionParser() (*typical.Parser[*Environment, bool], error) {
	spec := expression.DefaultParserSpec[*Environment]()

	if spec.Variables == nil {
		spec.Variables = make(map[string]typical.Variable)
	}

	spec.Variables["name"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetMetadata().GetName(), nil
	})
	spec.Variables["metadata.name"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetMetadata().GetName(), nil
	})
	spec.Variables["spec.bot_name"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetSpec().GetBotName(), nil
	})
	spec.Variables["spec.instance_id"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetSpec().GetInstanceId(), nil
	})
	spec.Variables["status.latest_heartbeat.architecture"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetLatestHeartbeat().GetArchitecture(), nil
	})
	spec.Variables["status.latest_heartbeat.os"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetLatestHeartbeat().GetOs(), nil
	})
	spec.Variables["status.latest_heartbeat.hostname"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetLatestHeartbeat().GetHostname(), nil
	})
	spec.Variables["status.latest_heartbeat.one_shot"] = typical.DynamicVariable(func(env *Environment) (bool, error) {
		return env.GetLatestHeartbeat().GetOneShot(), nil
	})
	spec.Variables["status.latest_heartbeat.version"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetLatestHeartbeat().GetVersion(), nil
	})
	spec.Variables["status.latest_authentication.join_method"] = typical.DynamicVariable(func(env *Environment) (string, error) {
		return env.GetLatestAuthentication().GetJoinMethod(), nil
	})

	spec.Functions["between"] = typical.TernaryFunction[*Environment](expression.SemverBetween)

	return typical.NewParser[*Environment, bool](spec)
}
