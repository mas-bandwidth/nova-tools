package ntable

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// The helpers below are reached only by tests, so they live in this test-only
// file and no shipped binary carries them.

// The original key helpers name epoch zero for existing callers.
func RowsKey(table string) string     { return RowsKeyAt(table, 0) }
func RowKey(table, row string) string { return RowKeyAt(table, row, 0) }

func RevisionKey(table string) string { return DefKey(table) + ":revision" }

// rowFields is one row's hash: its label, exclude and owner when set, and
// key:<col> for every bound cell.
func rowFields(t Table, r Row) map[string]string {
	m := map[string]string{}
	if r.Label != "" {
		m["label"] = r.Label
	}
	if r.Exclude != "" {
		m["exclude"] = r.Exclude
	}
	if r.Owner != "" {
		m["owner"] = r.Owner
	}
	for i, c := range t.Columns {
		if i < len(r.Cells) && r.Cells[i].Bound && r.Cells[i].Key != "" {
			m["key:"+c.Name] = r.Cells[i].Key
		}
	}
	return m
}

func List(ctx context.Context, c redis.Cmdable) ([]string, error) {
	rows, err := Summaries(ctx, c)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	return names, nil
}

// Single-member helpers use the same list operation and wire protocol.
func CellRemove(ctx context.Context, c redis.Cmdable, name, row, col, member string, opts ...WriteOptions) (int64, error) {
	return CellsRemove(ctx, c, name, row, col, []string{member}, opts...)
}
func CellMove(ctx context.Context, c redis.Cmdable, name, row, from, to, member string, opts ...WriteOptions) (int64, error) {
	return CellsMove(ctx, c, name, row, from, to, []string{member}, opts...)
}
