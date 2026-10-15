package store

import "testing"

func TestMigrateURL(t *testing.T) {
	for _, tc := range []struct{ dsn, want string }{
		{"postgres://argus:pw@db:5432/argus?sslmode=disable", "pgx5://argus:pw@db:5432/argus?sslmode=disable"},
		{"postgresql://argus@db/argus", "pgx5://argus@db/argus"},
	} {
		got, err := migrateURL(tc.dsn)
		if err != nil || got != tc.want {
			t.Errorf("migrateURL(%q) = %q, %v; want %q", tc.dsn, got, err, tc.want)
		}
	}
	// pgx takes the key=value form; migrations cannot, so it is refused with
	// a message instead of an error from inside the driver.
	for _, dsn := range []string{"host=db user=argus dbname=argus", "mysql://db/argus", ""} {
		if _, err := migrateURL(dsn); err == nil {
			t.Errorf("migrateURL(%q) accepted a DSN migrations cannot use", dsn)
		}
	}
}
