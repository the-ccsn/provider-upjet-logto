package client

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

// listPages returns a complete relation collection or an error; truncation is never absence.
func listPages[T any](ctx context.Context, c *Client, endpoint string, identifier func(T) string) ([]T, error) {
	const pageSize = 100
	result := []T{}
	seen := map[string]bool{}
	for page := 1; page <= 10000; page++ {
		res, err := expect(200)(c.do(ctx, &request{method: http.MethodGet, path: endpoint, queryParameters: map[string]string{"page": strconv.Itoa(page), "page_size": strconv.Itoa(pageSize)}}))
		if err != nil {
			return nil, err
		}
		var rows []T
		if err := decode(res.Body, &rows); err != nil {
			return nil, err
		}
		progress := false
		for _, row := range rows {
			id := identifier(row)
			if id == "" {
				return nil, errors.New("paginated response contains an empty identifier; retaining existing state")
			}
			if !seen[id] {
				seen[id] = true
				progress = true
				result = append(result, row)
			}
		}
		if len(rows) < pageSize {
			return result, nil
		}
		if !progress {
			return nil, errors.New("pagination made no progress; retaining existing state")
		}
	}
	return nil, errors.New("pagination limit exceeded; retaining existing state")
}
