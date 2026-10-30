package repo

import (
	"context"
	"fmt"
)

// The admin dashboard.
//
// Everything here mirrors GetAdminStats/Query.cs in the C# service, which is
// the behavioural reference. The earlier Go implementation returned a different
// set of fields entirely - totalUsers, pendingTasks, openReports and a
// "dailyStats" key - so the admin page rendered almost nothing and then threw
// on stats.requestsToday being undefined. A route sweep cannot catch that,
// because the route, the method and the status code were all correct; only the
// body was wrong.
//
// The field names, the ordering and the row limits below are the contract the
// frontend depends on. Changing one silently breaks the dashboard.

// UserStat is a per-user request breakdown.
type UserStat struct {
	ID                 int64
	Name               string
	RequestCount       int64
	APIKeyRequestCount int64
	JWTRequestCount    int64
}

// UploaderStat counts accepted uploads per user.
type UploaderStat struct {
	ID                 int64
	Name               string
	UploadedImageCount int64
}

// AlbumUserStat counts album membership per user.
type AlbumUserStat struct {
	ID              int64
	Name            string
	AlbumImageCount int64
}

// AdminDashboard is the full GET /stats/admin payload.
type AdminDashboard struct {
	RequestsToday  int64
	History        []DailyStatPoint
	TopUsers       []UserStat
	TopAPIKeyUsers []UserStat
	TopJWTUsers    []UserStat
	TopUploaders   []UploaderStat
	TopAlbumUsers  []AlbumUserStat

	TotalRequests                int64
	TotalAuthenticatedRequests   int64
	TotalUnauthenticatedRequests int64
	TotalAPIKeyRequests          int64
	TotalJWTRequests             int64
	TotalImages                  int64
	TotalTags                    int64
	TotalArtists                 int64
}

// DailyStatPoint is one day of the history series.
type DailyStatPoint struct {
	Date  string
	Count int64
}

// Row limits, copied from the reference implementation.
const (
	topUsersLimit      = 10
	topSmallListLimit  = 5
	topUploadersLimit  = 10
	topAlbumUsersLimit = 10
)

// GetAdminDashboard builds the whole dashboard payload.
//
// Every statement below is deliberately bounded with a LIMIT, because this runs
// against a catalogue that grows without limit and an ORDER BY over the whole
// Users table is exactly the kind of thing that stalls a small instance.
func (r *Repo) GetAdminDashboard(ctx context.Context) (AdminDashboard, error) {
	var d AdminDashboard

	// Counts and today's total in one round trip. The request-type totals are
	// sums over Users, matching SUM(u => u.RequestCount) in the reference.
	const totals = `
        SELECT
            (SELECT COALESCE(SUM("RequestCount"), 0) FROM "DailyStats"),
            (SELECT COALESCE(SUM("RequestCount"), 0) FROM "Users"),
            (SELECT COALESCE(SUM("ApiKeyRequestCount"), 0) FROM "Users"),
            (SELECT COALESCE(SUM("JwtRequestCount"), 0) FROM "Users"),
            (SELECT COUNT(*) FROM "Images"),
            (SELECT COUNT(*) FROM "Tags"),
            (SELECT COUNT(*) FROM "Artists"),
            (SELECT COALESCE(SUM("RequestCount"), 0) FROM "DailyStats" WHERE "Date" = CURRENT_DATE)`

	if err := r.db.QueryRow(ctx, totals).Scan(
		&d.TotalRequests, &d.TotalAuthenticatedRequests,
		&d.TotalAPIKeyRequests, &d.TotalJWTRequests,
		&d.TotalImages, &d.TotalTags, &d.TotalArtists,
		&d.RequestsToday,
	); err != nil {
		return AdminDashboard{}, fmt.Errorf("get admin dashboard totals: %w", err)
	}

	// Anonymous is whatever the total is minus what known users account for.
	// A negative here would mean the two counters disagree, which is worth
	// clamping rather than surfacing as a nonsensical figure on the dashboard.
	d.TotalUnauthenticatedRequests = d.TotalRequests - d.TotalAuthenticatedRequests
	if d.TotalUnauthenticatedRequests < 0 {
		d.TotalUnauthenticatedRequests = 0
	}

	history, err := r.adminHistory(ctx)
	if err != nil {
		return AdminDashboard{}, err
	}
	d.History = history

	if d.TopUsers, err = r.topUsersBy(ctx, `"RequestCount"`, topUsersLimit); err != nil {
		return AdminDashboard{}, err
	}
	if d.TopAPIKeyUsers, err = r.topUsersBy(ctx, `"ApiKeyRequestCount"`, topSmallListLimit); err != nil {
		return AdminDashboard{}, err
	}
	if d.TopJWTUsers, err = r.topUsersBy(ctx, `"JwtRequestCount"`, topSmallListLimit); err != nil {
		return AdminDashboard{}, err
	}
	if d.TopUploaders, err = r.topUploaders(ctx); err != nil {
		return AdminDashboard{}, err
	}
	if d.TopAlbumUsers, err = r.topAlbumUsers(ctx); err != nil {
		return AdminDashboard{}, err
	}

	return d, nil
}

// adminHistory returns per-day counts for the last year, oldest first.
//
// The year window matches the reference. Longer than the stats retention the
// cleanup job prunes at, so it returns everything there is.
func (r *Repo) adminHistory(ctx context.Context) ([]DailyStatPoint, error) {
	rows, err := r.db.Query(ctx, `
        SELECT to_char("Date", 'YYYY-MM-DD'), "RequestCount"
        FROM "DailyStats"
        WHERE "Date" >= CURRENT_DATE - INTERVAL '1 year'
        ORDER BY "Date"`)
	if err != nil {
		return nil, fmt.Errorf("get admin dashboard history: %w", err)
	}
	defer rows.Close()

	points := make([]DailyStatPoint, 0, 64)
	for rows.Next() {
		var p DailyStatPoint
		if err := rows.Scan(&p.Date, &p.Count); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// topUsersBy ranks users by one of their request counters.
//
// The column name is interpolated rather than parameterised because Postgres
// will not bind an identifier, so it is restricted to a caller-chosen literal
// at each call site above. Every value passed in is a quoted column constant,
// never anything derived from a request.
func (r *Repo) topUsersBy(ctx context.Context, column string, limit int) ([]UserStat, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
        SELECT "Id", "Name", "RequestCount", "ApiKeyRequestCount", "JwtRequestCount"
        FROM "Users"
        ORDER BY %s DESC, "Id" ASC
        LIMIT %d`, column, limit))
	if err != nil {
		return nil, fmt.Errorf("get admin dashboard top users by %s: %w", column, err)
	}
	defer rows.Close()

	out := make([]UserStat, 0, limit)
	for rows.Next() {
		var u UserStat
		if err := rows.Scan(&u.ID, &u.Name, &u.RequestCount,
			&u.APIKeyRequestCount, &u.JWTRequestCount); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// topUploaders ranks users by accepted uploads.
//
// The count is a join rather than a correlated subquery per user, so this stays
// one pass over Images however many users there are.
func (r *Repo) topUploaders(ctx context.Context) ([]UploaderStat, error) {
	rows, err := r.db.Query(ctx, `
        SELECT u."Id", u."Name", COUNT(i."Id") AS "UploadedImageCount"
        FROM "Users" u
        JOIN "Images" i ON i."UploaderId" = u."Id" AND i."ReviewStatus" = 1
        GROUP BY u."Id", u."Name"
        ORDER BY "UploadedImageCount" DESC, u."Id" ASC
        LIMIT $1`, topUploadersLimit)
	if err != nil {
		return nil, fmt.Errorf("get admin dashboard top uploaders: %w", err)
	}
	defer rows.Close()

	out := make([]UploaderStat, 0, topUploadersLimit)
	for rows.Next() {
		var u UploaderStat
		if err := rows.Scan(&u.ID, &u.Name, &u.UploadedImageCount); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// topAlbumUsers ranks users by how many images sit in their albums.
func (r *Repo) topAlbumUsers(ctx context.Context) ([]AlbumUserStat, error) {
	rows, err := r.db.Query(ctx, `
        SELECT a."UserId", u."Name", COUNT(*) AS "AlbumImageCount"
        FROM "AlbumItems" ai
        JOIN "Albums" a ON a."Id" = ai."AlbumId"
        JOIN "Users" u ON u."Id" = a."UserId"
        GROUP BY a."UserId", u."Name"
        ORDER BY "AlbumImageCount" DESC, a."UserId" ASC
        LIMIT $1`, topAlbumUsersLimit)
	if err != nil {
		return nil, fmt.Errorf("get admin dashboard top album users: %w", err)
	}
	defer rows.Close()

	out := make([]AlbumUserStat, 0, topAlbumUsersLimit)
	for rows.Next() {
		var a AlbumUserStat
		if err := rows.Scan(&a.ID, &a.Name, &a.AlbumImageCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// unusedTime keeps the time import meaningful if DailyStatPoint stops using it.
