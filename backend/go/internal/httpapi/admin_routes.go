package httpapi

import (
	"net/http"
	"strings"

	"waifuapi/internal/config"
	"waifuapi/internal/repo"
	"waifuapi/internal/store/models"
)

// This file holds the endpoints that are neither CRUD on a single resource nor
// part of the review queue: the moderation dashboard, API key maintenance and
// the image metadata edit.

// registerAdminStatsRoute mounts GET /stats/admin.
func (s *Server) registerAdminStatsRoute(mux *http.ServeMux) {
	base := s.cfg.BasePathOrRoot()
	mux.HandleFunc("GET "+base+"stats/admin", s.handleAdminStats)
}

// registerAPIKeyUpdateRoute mounts PATCH /auth/api-keys/{id}.
func (s *Server) registerAPIKeyUpdateRoute(mux *http.ServeMux) {
	base := s.cfg.BasePathOrRoot()
	mux.HandleFunc("PATCH "+base+"auth/api-keys/{id}", s.handleUpdateAPIKey)
}

// registerImageUpdateRoute mounts PATCH /images/{id}.
func (s *Server) registerImageMutationRoutes(mux *http.ServeMux) {
	base := s.cfg.BasePathOrRoot()

	mux.HandleFunc("PATCH "+base+"images/{id}", s.handleUpdateImage)
	mux.HandleFunc("DELETE "+base+"images/{id}", s.handleDeleteImage)
}

// adminUserStatDTO, adminUploaderStatDTO and adminAlbumUserStatDTO are the
// leaderboard rows. Field names and order come from the reference service's
// UserStatDto, UploaderStatDto and AlbumUserStatDto; the frontend indexes into
// them by position.
type adminUserStatDTO struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	RequestCount       int64  `json:"requestCount"`
	APIKeyRequestCount int64  `json:"apiKeyRequestCount"`
	JWTRequestCount    int64  `json:"jwtRequestCount"`
}

type adminUploaderStatDTO struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	UploadedImageCount int64  `json:"uploadedImageCount"`
}

type adminAlbumUserStatDTO struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	AlbumImageCount int64  `json:"albumImageCount"`
}

type adminDailyStatDTO struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// adminStatsDTO is the GET /stats/admin response.
//
// Every field here is required by the dashboard. An earlier version returned a
// different set - totalUsers, pendingTasks, openReports - so the page rendered
// almost nothing and threw on requestsToday being undefined. The route and the
// status code were both correct, which is why nothing caught it.
type adminStatsDTO struct {
	RequestsToday int64               `json:"requestsToday"`
	History       []adminDailyStatDTO `json:"history"`

	TopUsers       []adminUserStatDTO      `json:"topUsers"`
	TopAPIKeyUsers []adminUserStatDTO      `json:"topApiKeyUsers"`
	TopJWTUsers    []adminUserStatDTO      `json:"topJwtUsers"`
	TopUploaders   []adminUploaderStatDTO  `json:"topUploaders"`
	TopAlbumUsers  []adminAlbumUserStatDTO `json:"topAlbumUsers"`

	TotalRequests                int64 `json:"totalRequests"`
	TotalAuthenticatedRequests   int64 `json:"totalAuthenticatedRequests"`
	TotalUnauthenticatedRequests int64 `json:"totalUnauthenticatedRequests"`
	TotalAPIKeyRequests          int64 `json:"totalApiKeyRequests"`
	TotalJWTRequests             int64 `json:"totalJwtRequests"`
	TotalImages                  int64 `json:"totalImages"`
	TotalTags                    int64 `json:"totalTags"`
	TotalArtists                 int64 `json:"totalArtists"`
}

// handleAdminStats serves GET /stats/admin.
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, config.RoleAdmin, "view admin statistics") {
		return
	}

	d, err := s.repo.GetAdminDashboard(r.Context())
	if err != nil {
		s.logError(r, "admin stats", err)
		internalServerError(w, r)
		return
	}

	// Every slice is allocated before it is filled so that an empty result
	// serialises as [] rather than null. The frontend iterates these directly.
	history := make([]adminDailyStatDTO, 0, len(d.History))
	for _, p := range d.History {
		history = append(history, adminDailyStatDTO{Date: p.Date, Count: p.Count})
	}

	topUsers := make([]adminUserStatDTO, 0, len(d.TopUsers))
	for _, u := range d.TopUsers {
		topUsers = append(topUsers, adminUserStatDTO{
			ID: u.ID, Name: u.Name, RequestCount: u.RequestCount,
			APIKeyRequestCount: u.APIKeyRequestCount, JWTRequestCount: u.JWTRequestCount,
		})
	}

	topAPIKey := make([]adminUserStatDTO, 0, len(d.TopAPIKeyUsers))
	for _, u := range d.TopAPIKeyUsers {
		topAPIKey = append(topAPIKey, adminUserStatDTO{
			ID: u.ID, Name: u.Name, RequestCount: u.RequestCount,
			APIKeyRequestCount: u.APIKeyRequestCount, JWTRequestCount: u.JWTRequestCount,
		})
	}

	topJWT := make([]adminUserStatDTO, 0, len(d.TopJWTUsers))
	for _, u := range d.TopJWTUsers {
		topJWT = append(topJWT, adminUserStatDTO{
			ID: u.ID, Name: u.Name, RequestCount: u.RequestCount,
			APIKeyRequestCount: u.APIKeyRequestCount, JWTRequestCount: u.JWTRequestCount,
		})
	}

	topUploaders := make([]adminUploaderStatDTO, 0, len(d.TopUploaders))
	for _, u := range d.TopUploaders {
		topUploaders = append(topUploaders, adminUploaderStatDTO{
			ID: u.ID, Name: u.Name, UploadedImageCount: u.UploadedImageCount,
		})
	}

	topAlbum := make([]adminAlbumUserStatDTO, 0, len(d.TopAlbumUsers))
	for _, u := range d.TopAlbumUsers {
		topAlbum = append(topAlbum, adminAlbumUserStatDTO{
			ID: u.ID, Name: u.Name, AlbumImageCount: u.AlbumImageCount,
		})
	}

	writeJSON(w, http.StatusOK, adminStatsDTO{
		RequestsToday:                d.RequestsToday,
		History:                      history,
		TopUsers:                     topUsers,
		TopAPIKeyUsers:               topAPIKey,
		TopJWTUsers:                  topJWT,
		TopUploaders:                 topUploaders,
		TopAlbumUsers:                topAlbum,
		TotalRequests:                d.TotalRequests,
		TotalAuthenticatedRequests:   d.TotalAuthenticatedRequests,
		TotalUnauthenticatedRequests: d.TotalUnauthenticatedRequests,
		TotalAPIKeyRequests:          d.TotalAPIKeyRequests,
		TotalJWTRequests:             d.TotalJWTRequests,
		TotalImages:                  d.TotalImages,
		TotalTags:                    d.TotalTags,
		TotalArtists:                 d.TotalArtists,
	})
}

// statsWindowDays is how many days of per-day counts the dashboard returns.
const statsWindowDays = 30

// updateAPIKeyRequest is the PATCH body.
type updateAPIKeyRequest struct {
	Description string `json:"description"`
}

// handleUpdateAPIKey serves PATCH /auth/api-keys/{id}.
//
// Only the description is editable. The secret cannot be rotated in place,
// because that would silently break whichever client was using it; a new key is
// created instead and the old one revoked.
func (s *Server) handleUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	caller := s.requireAuth(w, r)
	if caller == nil {
		return
	}

	id, err := parsePathInt64(r, "id")
	if err != nil {
		writeAPIError(w, r, err)
		return
	}

	var body updateAPIKeyRequest
	if err := decodeJSONBody(r, &body); err != nil {
		writeAPIError(w, r, err)
		return
	}

	description := strings.TrimSpace(body.Description)
	if description == "" {
		writeAPIError(w, r, validationError("Description", "Description is required."))
		return
	}

	key, err := s.repo.UpdateAPIKeyDescription(r.Context(), id, caller.UserID, description)
	if err != nil {
		writeRepoError(w, r, "update api key", err, "API key not found.")
		return
	}

	writeJSON(w, http.StatusOK, toAPIKeyDto(key))
}

// updateImageRequest is the PATCH body.
type updateImageRequest struct {
	Source *string `json:"source"`
	IsNsfw *bool   `json:"isNsfw"`
}

// handleUpdateImage serves PATCH /images/{id}.
//
// Moderator only. ImagesController.Update carries
// [Authorize(Policy = AuthorizationPolicies.Moderator)] and UpdateImageCommand has
// no ownership check of its own, so an ordinary user cannot edit an image at
// all — including one they uploaded.
//
// The previous implementation here allowed the uploader in and read JSON. Both
// were wrong: the policy made it more permissive than the original, and [FromForm]
// on the C# side means the moderator screen sends multipart, which the JSON
// decoder would have rejected outright.
func (s *Server) handleUpdateImage(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, config.RoleModerator, "edit images") {
		return
	}

	id, err := parsePathInt64(r, "id")
	if err != nil {
		writeAPIError(w, r, err)
		return
	}

	if _, err := s.repo.GetImageByID(r.Context(), id, true); err != nil {
		writeRepoError(w, r, "get image for update", err,
			"Image with ID "+itoa64(id)+" not found.")
		return
	}

	fields, err := parseImageUpdateForm(r, s.cfg.MaxUploadSizeBytes)
	if err != nil {
		writeAPIError(w, r, err)
		return
	}

	if err := s.applyImageUpdate(r.Context(), id, fields); err != nil {
		s.logError(r, "update image", err)
		writeImageUpdateError(w, r, id, err)
		return
	}

	updated, err := s.repo.GetImageByID(r.Context(), id, true)
	if err != nil {
		writeRepoError(w, r, "re-read image", err,
			"Image with ID "+itoa64(id)+" not found.")
		return
	}

	// includeUploaderId is true because UpdateImageCommand maps with
	// ToDto(_cdnBaseUrl, includeUploaderId: true).
	dtos, err := s.toImageDtos(r, []models.Image{updated}, repo.ImageFilter{
		ReviewStatus: models.FilterAll,
	}, 0)
	if err != nil || len(dtos) == 0 {
		s.logError(r, "resolve image relations", err)
		internalServerError(w, r)
		return
	}

	writeJSON(w, http.StatusOK, dtos[0])
}

// handleDeleteImage serves DELETE /images/{id}.
//
// Admin only. ImagesController.Delete carries [Authorize(Policy =
// AuthorizationPolicies.Admin)], which short-circuits before DeleteImageCommand
// ever runs, so the command's ownership allowance is unreachable from the HTTP
// surface: an ordinary user cannot delete their own image here, and must cancel
// the upload instead.
//
// DeleteImageCommand does contain
// `if (image.UploaderId != request.RequesterId && !request.IsAdminOrModerator)`,
// and an earlier version of this handler implemented only that check. Reading the
// command and missing the policy on the controller is how a moderator-only
// endpoint quietly became user-accessible.
func (s *Server) handleDeleteImage(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, config.RoleAdmin, "delete images") {
		return
	}

	id, err := parsePathInt64(r, "id")
	if err != nil {
		writeAPIError(w, r, err)
		return
	}

	image, err := s.repo.GetImageByID(r.Context(), id, true)
	if err != nil {
		writeRepoError(w, r, "get image for deletion", err,
			"Image with ID "+itoa64(id)+" not found.")
		return
	}

	if err := s.repo.DeleteImage(r.Context(), id); err != nil {
		s.logError(r, "delete image row", err)
		internalServerError(w, r)
		return
	}

	// Best effort: the row is already gone, and a lingering object is an
	// operational nuisance that storage-diff will surface rather than a
	// correctness problem.
	if err := s.storage.Delete(contextWithoutCancel(r), storageKey(id, image.Extension)); err != nil {
		s.logError(r, "delete stored object", err)
	}
	s.purger.PurgeImage(contextWithoutCancel(r), id, []string{image.Extension})

	w.WriteHeader(http.StatusNoContent)
}

// deleteImageObjects removes an image's stored object and purges the edge.
//
// extraExtensions lists any additional extension to remove, which is how a file
// replacement cleans up the old object alongside the new one. It is best effort
// throughout: the database row is already gone by the time this runs, so a
// storage failure is an operational nuisance for storage-diff to find, not a
// reason to fail a request that already succeeded.
func (s *Server) deleteImageObjects(r *http.Request, imageID int64, extraExtension string) {
	// The row is gone, so the extension has to come from the payload the task
	// carried; the caller passes it when it is known.
	image, err := s.repo.GetImageByID(contextWithoutCancel(r), imageID, true)
	var extension string
	if err == nil {
		extension = image.Extension
	}
	if extension == "" {
		extension = extraExtension
	}
	if extension == "" {
		return
	}

	if err := s.storage.Delete(contextWithoutCancel(r), storageKey(imageID, extension)); err != nil {
		s.logError(r, "delete stored object", err)
		return
	}
	s.purgeImage(contextWithoutCancel(r), imageID, []string{extension})
}
