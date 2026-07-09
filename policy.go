package tracecheck

// policyMatches reports whether r satisfies every When constraint on rule.
// Within a field, any listed value matches (OR). Across fields, all must match
// (AND). An empty When matches every requirement.
//
// Special field names:
//   - "KeywordClass" matches Requirement.Class (must/should/may/implicit/…)
//   - any other name matches Requirement.Meta[name] (catalog metadata fields)
func policyMatches(rule PolicyRule, r Requirement) bool {
	if len(rule.When) == 0 {
		return true
	}
	for field, values := range rule.When {
		if len(values) == 0 {
			continue
		}
		var got string
		switch field {
		case "KeywordClass":
			got = r.Class
		default:
			got = r.MetaValue(field)
		}
		if !stringIn(values, got) {
			return false
		}
	}
	return true
}

func stringIn(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
