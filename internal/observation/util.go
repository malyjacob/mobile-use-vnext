package observation

import "sort"

// sortStrings sorts in place. Maps in the delta are unordered; sorting the keys
// before walking them keeps construction and error reporting deterministic.
func sortStrings(values []string) { sort.Strings(values) }
