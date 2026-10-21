package openai

// Price is the list price of a model in USD per million tokens. Text tokens use Input,
// CachedInput and Output. Image tokens use ImageInput and ImageOutput.
type Price struct {
	Input       float64
	CachedInput float64
	Output      float64
	ImageInput  float64
	ImageOutput float64
}

// PriceTable maps a model name, as sent in requests, to its price.
type PriceTable map[string]Price

// USD returns the cost of a Responses call at price p.
func (u Usage) USD(p Price) float64 {
	return (float64(u.InputTokens-u.CachedTokens)*p.Input +
		float64(u.CachedTokens)*p.CachedInput +
		float64(u.OutputTokens)*p.Output) / 1e6
}

// USD returns the cost of an Images call at price p.
func (u ImageUsage) USD(p Price) float64 {
	return (float64(u.InputTextTokens)*p.Input +
		float64(u.InputImageTokens)*p.ImageInput +
		float64(u.OutputTextTokens)*p.Output +
		float64(u.OutputImageTokens)*p.ImageOutput) / 1e6
}
