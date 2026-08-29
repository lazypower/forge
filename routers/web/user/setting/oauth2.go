// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/templates"
	"gitea.dev/services/context"
)

const (
	tplSettingsOAuthApplicationEdit templates.TplName = "user/settings/applications_oauth2_edit"
)

func newOAuth2CommonHandlers(userID int64) *OAuth2CommonHandlers {
	return &OAuth2CommonHandlers{
		OwnerID:            userID,
		BasePathList:       setting.AppSubURL + "/user/settings/applications",
		BasePathEditPrefix: setting.AppSubURL + "/user/settings/applications/oauth2",
		TplAppEdit:         tplSettingsOAuthApplicationEdit,
	}
}

// OAuthApplicationsPost response for adding a oauth2 application
func OAuthApplicationsPost(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings_title")
	ctx.Data["PageIsSettingsApplications"] = true

	oa := newOAuth2CommonHandlers(ctx.Doer.ID)
	oa.AddApp(ctx)
}

// OAuthApplicationsEdit response for editing oauth2 application
func OAuthApplicationsEdit(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings_title")
	ctx.Data["PageIsSettingsApplications"] = true

	oa := newOAuth2CommonHandlers(ctx.Doer.ID)
	oa.EditSave(ctx)
}

// OAuthApplicationsRegenerateSecret handles the post request for regenerating the secret
func OAuthApplicationsRegenerateSecret(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings_title")
	ctx.Data["PageIsSettingsApplications"] = true

	oa := newOAuth2CommonHandlers(ctx.Doer.ID)
	oa.RegenerateSecret(ctx)
}

// OAuth2ApplicationShow displays the given application
func OAuth2ApplicationShow(ctx *context.Context) {
	oa := newOAuth2CommonHandlers(ctx.Doer.ID)
	oa.EditShow(ctx)
}

// DeleteOAuth2Application deletes the given oauth2 application
func DeleteOAuth2Application(ctx *context.Context) {
	oa := newOAuth2CommonHandlers(ctx.Doer.ID)
	oa.DeleteApp(ctx)
}

// DeleteMCPRegistration deletes an inert finalized registration bound to the current principal.
func DeleteMCPRegistration(ctx *context.Context) {
	if err := auth_model.DeleteFinalizedMCPRegistration(ctx, ctx.PathParamInt64("id"), ctx.Doer.ID); err != nil {
		ctx.Flash.Error(ctx.Tr("settings.mcp_registration_delete_failed"))
	} else {
		ctx.Flash.Success(ctx.Tr("settings.mcp_registration_delete_success"))
	}
	ctx.JSONRedirect(setting.AppSubURL + "/user/settings/applications")
}

// RevokeOAuth2Grant revokes the grant with the given id
func RevokeOAuth2Grant(ctx *context.Context) {
	oa := newOAuth2CommonHandlers(ctx.Doer.ID)
	oa.RevokeGrant(ctx)
}
