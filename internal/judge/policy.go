package judge

// Policy maps rule probabilities to a decision.
type Policy struct {
	ReviewAt float64
	RejectAt float64
}

// DefaultPolicy uses the starting thresholds recommended for Nouls.
func DefaultPolicy() Policy {
	return Policy{ReviewAt: 0.30, RejectAt: 0.70}
}

// Decide combines the rule probabilities with the max-rule policy.
func (p Policy) Decide(assessment *Assessment) Verdict {
	p = p.normalized()
	if assessment == nil || len(assessment.Rules) == 0 {
		return Verdict{Decision: Review}
	}

	var (
		maxRule string
		maxProb float64
	)
	for rule, probability := range assessment.Rules {
		if probability > maxProb || maxRule == "" {
			maxRule = rule
			maxProb = probability
		}
	}

	decision := Allow
	switch {
	case maxProb >= p.RejectAt:
		decision = Reject
	case maxProb >= p.ReviewAt:
		decision = Review
	}

	return Verdict{
		Decision:       decision,
		MaxRule:        maxRule,
		MaxProbability: maxProb,
		Findings:       sortedFindings(assessment.Rules, p.ReviewAt),
	}
}

func (p Policy) normalized() Policy {
	if p.ReviewAt <= 0 {
		p.ReviewAt = 0.30
	}
	if p.RejectAt <= 0 {
		p.RejectAt = 0.70
	}
	if p.RejectAt <= p.ReviewAt {
		p = DefaultPolicy()
	}
	return p
}
