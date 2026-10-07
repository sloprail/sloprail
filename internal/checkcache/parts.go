package checkcache

// ReasonRuleGone is why MigrateFromParts leaves a verdict behind: its rule is no longer declared.
const ReasonRuleGone = "rule no longer declared"

// PartsKey derives a guard's fingerprint from the parts a record stored beside it (Check.FilesPart
// and Check.SubjectFingerprint). It is the engine's: this package only keeps the parts.
type PartsKey func(files []byte, subjectFingerprint string) string

// HasParts says the check was stored with what its fingerprint was made of. A check whose
// parts are both empty (a record of an older build, or a subject naming no file and giving no
// fingerprint) has none to rewrite from, and is re-keyed the long way (Rebuild).
func (c Check) HasParts() bool { return len(c.FilesPart) > 0 || c.SubjectFingerprint != "" }

// MigrateFromParts re-keys every passing guard verdict that carries its parts by deriving the
// fingerprint again from them: a rewrite of the records, which reads nothing from the
// repository, runs no script and judges nothing, so it costs one hash per verdict. A verdict of
// a rule that declared() says is gone is left behind and counted as skipped, as Rebuild does.
// A check without parts is left as it is, for the caller to re-key by other means. The result
// holds copies: the runs given are never edited.
func MigrateFromParts(old []Run, declared func(rule string) bool, key PartsKey, passStatus, guardKind string) ([]Run, MigrationStats) {
	var st MigrationStats
	out := make([]Run, len(old))
	copy(out, old)
	for i, r := range old {
		owned := false
		for j, c := range r.Checks {
			if c.Kind != guardKind || c.Status != passStatus || c.Fingerprint == "" || !c.HasParts() {
				continue
			}
			if !declared(r.Rule) {
				st.Skip(ReasonRuleGone)
				continue
			}
			if !owned { // never edit the caller's run
				out[i].Checks = append([]Check(nil), r.Checks...)
				owned = true
			}
			out[i].Checks[j].Fingerprint = key(c.FilesPart, c.SubjectFingerprint)
			st.Migrated++
		}
	}
	return out, st
}
