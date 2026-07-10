package protocol

type FailoverConfig struct {
	MaxRetries       int
	ShouldFailover   func(model, vendor string, err error) bool
	GetFailoverModel func(model, vendor string) (string, string, bool)
}

func (c FailoverConfig) shouldFailover(model, vendor string, err error) bool {
	if c.ShouldFailover == nil {
		return true
	}
	return c.ShouldFailover(model, vendor, err)
}

func (c FailoverConfig) getFailoverModel(model, vendor string) (string, string, bool) {
	if c.GetFailoverModel == nil {
		return "", "", false
	}
	return c.GetFailoverModel(model, vendor)
}

func (c FailoverConfig) maxRetries() int {
	if c.MaxRetries <= 0 {
		return 2
	}
	return c.MaxRetries
}

func defaultFailoverConfig() FailoverConfig {
	return FailoverConfig{
		MaxRetries: 2,
		ShouldFailover: func(model, vendor string, err error) bool {
			return true
		},
		GetFailoverModel: func(model, vendor string) (string, string, bool) {
			return "", "", false
		},
	}
}
