package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSoleOwnerIsTheOneOtherRoleThatOwnsSchemaConfigWhole(t *testing.T) {
	t.Parallel()
	whole := Ownership{Role: "nova_admin", SchemaOwner: "nova_config", Tables: map[string]string{"fleet": "nova_config", "friends": "nova_config"}}
	owner, ok := SoleOwner(whole)
	assert.True(t, ok)
	assert.Equal(t, "nova_config", owner)

	mixed := Ownership{Role: "nova_config", SchemaOwner: "nova_admin", Create: true, Tables: map[string]string{"fleet": "nova_admin", "routes": "nova_config"}}
	_, ok = SoleOwner(mixed)
	assert.False(t, ok, "tables of mixed owners: no one role runs migrate as the catalog stands")

	own := Ownership{Role: "nova_config", SchemaOwner: "nova_config", Create: true, Tables: map[string]string{"fleet": "nova_config"}}
	_, ok = SoleOwner(own)
	assert.False(t, ok, "the role owns everything itself: nothing to run as")

	fresh := Ownership{Role: "nova_config"}
	_, ok = SoleOwner(fresh)
	assert.False(t, ok, "no schema yet")
}

func TestMigrateAsSwapsTheUserAndCarriesNoPassword(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "postgres://nova_config@127.0.0.1:5432/nova?sslmode=disable", MigrateAs("postgres://nova_admin:s3cret@127.0.0.1:5432/nova?sslmode=disable", "nova_config"))
	assert.Equal(t, "host=db port=5432 user=nova_config dbname=nova", MigrateAs("host=db port=5432 user=nova_admin password=s3cret dbname=nova", "nova_config"))
	assert.Equal(t, "host=db user=nova_config", MigrateAs("host=db", "nova_config"))
}

func TestWholeOwnerIsTheOneRoleOwningTheSchemaAndEveryTable(t *testing.T) {
	t.Parallel()
	whole := Ownership{Role: "nova_admin", SchemaOwner: "nova_config", Tables: map[string]string{"sprint": "nova_config", "fleet": "nova_config"}}
	owner, ok := WholeOwner(whole)
	assert.True(t, ok)
	assert.Equal(t, "nova_config", owner, "the owner is read whatever role is connected")

	self := whole
	self.Role = "nova_config"
	owner, ok = WholeOwner(self)
	assert.True(t, ok, "the connected role owning everything is the whole owner too (SoleOwner says no: it names another role)")
	assert.Equal(t, "nova_config", owner)
	_, sole := SoleOwner(self)
	assert.False(t, sole)

	mixed := Ownership{Role: "nova_config", SchemaOwner: "nova_config", Tables: map[string]string{"sprint": "nova_admin", "fleet": "nova_config"}}
	_, ok = WholeOwner(mixed)
	assert.False(t, ok, "a table of another owner is mixed ownership")

	_, ok = WholeOwner(Ownership{Role: "nova_config", Tables: map[string]string{}})
	assert.False(t, ok, "no schema yet has no owner")

	assert.Equal(t, "nova_admin pid=42 app=nova-sprint", Session{Role: "nova_admin", PID: 42, Application: "nova-sprint"}.String())
	assert.Equal(t, "nova_admin pid=42", Session{Role: "nova_admin", PID: 42}.String())
}
