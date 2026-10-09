package analyzer

// DefaultExcludedTags seeds the "excluded_tags" collection when it is empty.
// Broad draft rule: a market with any of these tags is not awesome, every
// other market is — when unsure, a market counts as awesome. Tags are
// Polymarket labels and are matched exactly (case-sensitive).
var DefaultExcludedTags = []string{
	// Auto-generated series: millions of near-identical markets.
	"Sports", "Games", "Esports", "Recurring", "Crypto Prices", "Up or Down", "Weather", "Tweet Markets",
	// Labeled "not awesome" during research.
	"Elections", "Global Elections", "Economy", "Macro Indicators", "Earnings Calls",
}
