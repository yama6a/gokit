package gemini

// Price is the list price of a model in USD per million tokens. Input covers text and
// image input. Output covers text output and thought tokens.
type Price struct {
	Input       float64
	CachedInput float64
	Output      float64
	ImageOutput float64
}

// PriceTable maps a model name, as sent in requests, to its price.
type PriceTable map[string]Price

// USD returns the cost of one call at price p.
func (u Usage) USD(p Price) float64 {
	return (float64(u.InputTokens-u.CachedTokens)*p.Input +
		float64(u.CachedTokens)*p.CachedInput +
		float64(u.OutputTokens-u.OutputImageTokens+u.ThoughtTokens)*p.Output +
		float64(u.OutputImageTokens)*p.ImageOutput) / 1e6
}
