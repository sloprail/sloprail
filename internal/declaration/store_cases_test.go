package declaration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sr-test cases live in the owning rule's folder (<nature>/<rule>/tests/<case>/) and in
// file-guard/structure.tests/<case>/. Nothing under them is a declaration: a case may carry
// declaration-looking files (the rules it runs against), and none of them is loaded, none is
// invalid, none is a rule.
func TestLoad_CaseFoldersAddNoRules(t *testing.T) {
	root := writeDecl(t, map[string]string{
		"gate/real/gate.yaml":                          "on:\n  - event: PreFileWrite\n    match: event.path startsWith \"x/\"\nrequire:\n  - skill: s\n",
		"gate/real/tests/c1/gate.yaml":                 "bogus: [",
		"gate/real/tests/c1/test.sh":                   "#!/bin/sh\n",
		"file-guard/structure.tests/c4/test.sh":        "#!/bin/sh\n",
		"file-guard/structure.tests/c4/gate.yaml":      "bogus: [",
		"file-guard/structure.tests/c4/structure.yaml": "bogus: [",
	})
	loaded, err := New(root).Load(testRegistry(t))
	require.NoError(t, err)
	assert.Empty(t, loaded.Invalid, "%v", invalidReasons(loaded))
	assert.Empty(t, loaded.FileGuards, "structure.tests/ is not a file-guard rule")
	assert.Empty(t, loaded.Contexts)
	assert.Nil(t, loaded.ProjectStructure(), "a structure.yaml inside a case is not the structure gate")
	require.Len(t, loaded.Gates, 1)
	assert.Equal(t, "real", loaded.Gates[0].Name)
}
