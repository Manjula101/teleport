/*
 * Teleport
 * Copyright (C) 2025  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package mysql

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/defaults"
)

func Test_getHealthCheckDBUser(t *testing.T) {
	rdsDB, err := types.NewDatabaseV3(types.Metadata{
		Name: "rds",
	}, types.DatabaseSpecV3{
		Protocol: defaults.ProtocolMySQL,
		URI:      "aurora-instance-1.abcdefghijklmnop.us-west-1.rds.amazonaws.com:5432",
	})
	require.NoError(t, err)
	cloudSQL := makeGCPMySQLDatabase(t)

	tests := []struct {
		desc     string
		database types.Database
		env      string
		admin    string
		want     string
	}{
		{
			desc:     "rds default",
			database: rdsDB,
			want:     "healthchecker",
		},
		{
			desc:     "rds with env",
			database: rdsDB,
			env:      "env",
			want:     "env",
		},
		{
			desc:     "rds with admin",
			database: rdsDB,
			admin:    "admin",
			want:     "admin",
		},
		{
			desc:     "rds with env and admin uses admin",
			database: rdsDB,
			env:      "env",
			admin:    "admin",
			want:     "admin",
		},
		{
			desc:     "gcp default",
			database: cloudSQL,
			want:     "healthchecker@project-1.iam.gserviceaccount.com",
		},
		{
			desc:     "gcp with env",
			database: cloudSQL,
			env:      "env",
			want:     "env@project-1.iam.gserviceaccount.com",
		},
		{
			desc:     "gcp with admin",
			database: cloudSQL,
			admin:    "admin",
			want:     "admin@project-1.iam.gserviceaccount.com",
		},
		{
			desc:     "gcp with env and admin uses admin",
			database: cloudSQL,
			env:      "env",
			admin:    "admin",
			want:     "admin@project-1.iam.gserviceaccount.com",
		},
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			t.Setenv(healthCheckUserEnvVar, test.env)
			db := test.database.Copy()
			db.Spec.AdminUser = &types.DatabaseAdminUser{Name: test.admin}
			got := getHealthCheckDBUser(db)
			require.Equal(t, test.want, got)
		})
	}
}
