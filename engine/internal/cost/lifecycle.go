package cost

import "sort"

// LifecycleRule is a proposed object-storage class transition: move whatever
// currently sits in FromClass to the cheaper ToClass.
type LifecycleRule struct {
	FromClass string
	ToClass   string
}

// Recommendation is a priced lifecycle transition: what the current bytes cost
// today vs. in the target class, and the monthly saving.
type Recommendation struct {
	FromClass        string  `json:"from_class"`
	ToClass          string  `json:"to_class"`
	GB               float64 `json:"gb"`
	CurrentMonthly   float64 `json:"current_monthly"`
	ProjectedMonthly float64 `json:"projected_monthly"`
	SavingsMonthly   float64 `json:"savings_monthly"`
}

// DefaultLifecycleRules are the common cold-tiering transition candidates.
// They are only candidates: LifecycleSavings keeps a transition only when the
// source class holds bytes and the target is priced and cheaper on the user's
// own pricing, so listing a candidate never forces a recommendation.
func DefaultLifecycleRules() []LifecycleRule {
	return []LifecycleRule{
		{FromClass: "STANDARD", ToClass: "STANDARD_IA"},
		{FromClass: "STANDARD", ToClass: "GLACIER_IR"},
		{FromClass: "STANDARD", ToClass: "GLACIER"},
		{FromClass: "STANDARD", ToClass: "DEEP_ARCHIVE"},
		{FromClass: "STANDARD_IA", ToClass: "GLACIER_IR"},
		{FromClass: "GLACIER_IR", ToClass: "GLACIER"},
		{FromClass: "GLACIER", ToClass: "DEEP_ARCHIVE"},
	}
}

// LifecycleSavings prices each transition against the current inventory,
// returning only real savings, biggest first. Deterministic.
//
// A recommendation is emitted only when the source class actually holds bytes
// and the target class is both priced and cheaper — an unpriced target is
// unknown, not free, and is never recommended. The user decides whether the
// data is cold enough to tolerate the target class's retrieval characteristics
// (that judgement is theirs; Argus only prices it).
func LifecycleSavings(storage []StorageObject, transitions []LifecycleRule, p *Pricing) []Recommendation {
	bytesByClass := map[string]int64{}
	for _, o := range storage {
		bytesByClass[o.Class] += o.Bytes
	}

	var recs []Recommendation
	for _, t := range transitions {
		bytes := bytesByClass[t.FromClass]
		if bytes == 0 {
			continue
		}
		fromRate, fromOK := p.Storage.PerGBMonthByClass[t.FromClass]
		toRate, toOK := p.Storage.PerGBMonthByClass[t.ToClass]
		if !fromOK || !toOK || toRate >= fromRate {
			continue
		}
		gb := float64(bytes) / bytesPerGB
		current := gb * fromRate
		projected := gb * toRate
		recs = append(recs, Recommendation{
			FromClass:        t.FromClass,
			ToClass:          t.ToClass,
			GB:               gb,
			CurrentMonthly:   current,
			ProjectedMonthly: projected,
			SavingsMonthly:   current - projected,
		})
	}

	sort.Slice(recs, func(i, j int) bool {
		if recs[i].SavingsMonthly != recs[j].SavingsMonthly {
			return recs[i].SavingsMonthly > recs[j].SavingsMonthly
		}
		if recs[i].FromClass != recs[j].FromClass {
			return recs[i].FromClass < recs[j].FromClass
		}
		return recs[i].ToClass < recs[j].ToClass
	})
	return recs
}
