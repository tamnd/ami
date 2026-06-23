package seed

import "context"

// sliceSource wraps a URL slice as a seed.Source.
type sliceSource struct {
	urls []string
}

// Slice returns a Source that yields each url as a Seed in order.
func Slice(urls []string) Source {
	return &sliceSource{urls: urls}
}

func (s *sliceSource) Name() string { return "slice" }

func (s *sliceSource) Iterate(ctx context.Context, yield func(Seed) error) error {
	for _, u := range s.urls {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := yield(Seed{URL: u}); err != nil {
			return err
		}
	}
	return nil
}
