package auth

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/organization"
)

// ErrInvalidOrgSlug: the slug is not 3–30 lowercase ASCII letters, digits or hyphens starting and
// ending with a letter or digit.
var ErrInvalidOrgSlug = errors.New("auth: organization slug must be 3-30 lowercase letters, digits or hyphens")

// orgSlugRegexp is the handle rule (HANDLE_SLUG_RE in the TS source): the pattern itself enforces
// the 3–30 length (1 + 1..28 + 1). internal/bookings.validateHandle delegates here so the public
// /book/{handle} segment and Limen's organization slug can never disagree about what is allowed.
var orgSlugRegexp = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`)

// ValidateOrgSlug reports ErrInvalidOrgSlug unless slug satisfies orgSlugRegexp.
func ValidateOrgSlug(slug string) error {
	if !orgSlugRegexp.MatchString(slug) {
		return ErrInvalidOrgSlug
	}
	return nil
}

// orgSlugLimenError is what the organization hooks return so Limen's responder answers 422 with a
// readable message (a plain error would become a 500).
func orgSlugLimenError() error {
	return limen.NewLimenError("slug must be 3-30 lowercase letters, digits or hyphens, starting and ending with a letter or digit", http.StatusUnprocessableEntity, nil)
}

// organizationHooks enforces the slug rule on Limen's own organization routes (POST /organizations/,
// PATCH /organizations/:id), which otherwise only TrimSpace the slug (normalizeSlugs is off) —
// without this a raw PATCH could store "Foo Bar!!" as the public /book/<handle> segment. Both
// hooks run AFTER Limen's own slug generation/normalization (organizations.go), so request.Slug is
// the final value. The personal-org creation path goes through CreateOrganization too, which is
// why personalOrgSlug caps its length (see its doc comment).
func organizationHooks() organization.Hooks {
	return organization.Hooks{
		BeforeCreateOrganization: func(_ context.Context, _ *limen.User, req *organization.CreateOrganizationRequest) error {
			if err := ValidateOrgSlug(req.Slug); err != nil {
				return orgSlugLimenError()
			}
			return nil
		},
		BeforeUpdateOrganization: func(_ context.Context, _ *limen.User, _ *organization.Organization, req *organization.UpdateOrganizationRequest) error {
			if req.Slug != nil {
				if err := ValidateOrgSlug(*req.Slug); err != nil {
					return orgSlugLimenError()
				}
			}
			return nil
		},
	}
}
