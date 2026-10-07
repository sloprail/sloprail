package checkcache

// PutAsOlder files runs the way a build of an older schema did: in that schema's directory,
// under the ids its key made (version "sr1" held the rule's hash, "sr2" did not). It exists so
// that a test of a migration (here or in a package that supplies the Rebuild) can start from a
// real older store; nothing in the engine calls it. The runs' checks must already carry the
// fingerprints that schema gave them.
func PutAsOlder(opt Options, dir, version string, runs ...Run) error {
	s, err := Open(opt)
	if err != nil {
		return err
	}
	s.dir = dir
	s.keyID = func(r Run, c Check) string {
		if version == "sr1" {
			return legacyID(r.CheckKey(c), r.RuleHash)
		}
		return r.CheckKey(c).idUnder(version)
	}
	for _, r := range runs {
		if err := s.Put([]Run{r}); err != nil {
			return err
		}
	}
	return nil
}
