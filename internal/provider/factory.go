package provider

import "fmt"

func New(name string, mock bool) (Runner, error) {
	if mock {
		return NewMockProvider(0), nil
	}

	switch name {
	case "", "opencode":
		return NewOpenCodeProvider(), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", name)
	}
}
