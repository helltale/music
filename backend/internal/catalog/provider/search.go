package provider

import "context"

// SearchList queries each catalog provider in order.
// Items are capped by limit. total is the sum of the providers' match counts.
type SearchList []ArtistSearcher

// SearchArtists returns hits from every provider. The first error stops the search.
func (s SearchList) SearchArtists(ctx context.Context, query string, limit int) ([]ArtistCandidate, int, error) {
	items := make([]ArtistCandidate, 0)
	total := 0
	for _, searcher := range s {
		found, count, err := searcher.SearchArtists(ctx, query, limit)
		if err != nil {
			return nil, 0, err
		}
		total += count
		items = append(items, found...)
	}
	if limit < 0 {
		limit = 0
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, total, nil
}
