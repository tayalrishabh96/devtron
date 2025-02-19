/*
 * Copyright (c) 2020 Devtron Labs
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package restHandler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/devtron-labs/devtron/api/restHandler/common"
	"github.com/devtron-labs/devtron/internal/sql/repository"
	"github.com/devtron-labs/devtron/pkg/auth/authorisation/casbin"
	"github.com/devtron-labs/devtron/pkg/auth/user"
	"github.com/devtron-labs/devtron/pkg/cluster"
	"github.com/devtron-labs/devtron/pkg/notifier"
	"github.com/devtron-labs/devtron/pkg/pipeline"
	"github.com/devtron-labs/devtron/pkg/team"
	util "github.com/devtron-labs/devtron/util/event"
	"github.com/devtron-labs/devtron/util/rbac"
	"github.com/devtron-labs/devtron/util/response"
	"github.com/go-pg/pg"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
	"gopkg.in/go-playground/validator.v9"
)

const (
	SLACK_CONFIG_DELETE_SUCCESS_RESP   = "Slack config deleted successfully."
	WEBHOOK_CONFIG_DELETE_SUCCESS_RESP = "Webhook config deleted successfully."
	SES_CONFIG_DELETE_SUCCESS_RESP     = "SES config deleted successfully."
	SMTP_CONFIG_DELETE_SUCCESS_RESP    = "SMTP config deleted successfully."
)

type NotificationRestHandler interface {
	SaveNotificationSettings(w http.ResponseWriter, r *http.Request)
	UpdateNotificationSettings(w http.ResponseWriter, r *http.Request)
	SaveNotificationChannelConfig(w http.ResponseWriter, r *http.Request)
	FindSESConfig(w http.ResponseWriter, r *http.Request)
	FindSlackConfig(w http.ResponseWriter, r *http.Request)
	FindSMTPConfig(w http.ResponseWriter, r *http.Request)
	FindWebhookConfig(w http.ResponseWriter, r *http.Request)
	GetWebhookVariables(w http.ResponseWriter, r *http.Request)
	FindAllNotificationConfig(w http.ResponseWriter, r *http.Request)
	GetAllNotificationSettings(w http.ResponseWriter, r *http.Request)
	DeleteNotificationSettings(w http.ResponseWriter, r *http.Request)
	DeleteNotificationChannelConfig(w http.ResponseWriter, r *http.Request)

	RecipientListingSuggestion(w http.ResponseWriter, r *http.Request)
	FindAllNotificationConfigAutocomplete(w http.ResponseWriter, r *http.Request)
	GetOptionsForNotificationSettings(w http.ResponseWriter, r *http.Request)
}
type NotificationRestHandlerImpl struct {
	dockerRegistryConfig pipeline.DockerRegistryConfig
	logger               *zap.SugaredLogger
	gitRegistryConfig    pipeline.GitRegistryConfig
	dbConfigService pipeline.DbConfigService
	userAuthService user.UserService
	validator       *validator.Validate
	notificationService  notifier.NotificationConfigService
	slackService         notifier.SlackNotificationService
	webhookService       notifier.WebhookNotificationService
	sesService           notifier.SESNotificationService
	smtpService          notifier.SMTPNotificationService
	enforcer             casbin.Enforcer
	teamService          team.TeamService
	environmentService   cluster.EnvironmentService
	pipelineBuilder      pipeline.PipelineBuilder
	enforcerUtil         rbac.EnforcerUtil
}

type ChannelDto struct {
	Channel util.Channel `json:"channel" validate:"required"`
}

func NewNotificationRestHandlerImpl(dockerRegistryConfig pipeline.DockerRegistryConfig,
	logger *zap.SugaredLogger, gitRegistryConfig pipeline.GitRegistryConfig,
	dbConfigService pipeline.DbConfigService, userAuthService user.UserService,
	validator *validator.Validate, notificationService notifier.NotificationConfigService,
	slackService notifier.SlackNotificationService, webhookService notifier.WebhookNotificationService, sesService notifier.SESNotificationService, smtpService notifier.SMTPNotificationService,
	enforcer casbin.Enforcer, teamService team.TeamService, environmentService cluster.EnvironmentService, pipelineBuilder pipeline.PipelineBuilder,
	enforcerUtil rbac.EnforcerUtil) *NotificationRestHandlerImpl {
	return &NotificationRestHandlerImpl{
		dockerRegistryConfig: dockerRegistryConfig,
		logger:               logger,
		gitRegistryConfig:    gitRegistryConfig,
		dbConfigService:      dbConfigService,
		userAuthService:      userAuthService,
		validator:            validator,
		notificationService:  notificationService,
		slackService:         slackService,
		webhookService:       webhookService,
		sesService:           sesService,
		smtpService:          smtpService,
		enforcer:             enforcer,
		teamService:          teamService,
		environmentService:   environmentService,
		pipelineBuilder:      pipelineBuilder,
		enforcerUtil:         enforcerUtil,
	}
}

func (impl NotificationRestHandlerImpl) SaveNotificationSettings(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	var notificationSetting notifier.NotificationRequest
	err = json.NewDecoder(r.Body).Decode(&notificationSetting)
	if err != nil {
		impl.logger.Errorw("request err, SaveNotificationSettings", "err", err, "payload", notificationSetting)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	impl.logger.Infow("request payload, SaveNotificationSettings", "err", err, "payload", notificationSetting)
	err = impl.validator.Struct(notificationSetting)
	if err != nil {
		impl.logger.Errorw("validation err, SaveNotificationSettings", "err", err, "payload", notificationSetting)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	//RBAC
	token := r.Header.Get("token")
	for _, item := range notificationSetting.NotificationConfigRequest {
		teamRbac, envRbac := impl.buildRbacObjectsForNotificationSettings(item.TeamId, item.EnvId, item.AppId, item.PipelineId, item.PipelineType)
		for _, object := range teamRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceApplications, casbin.ActionCreate, object); !ok {
				common.WriteJsonResp(w, fmt.Errorf("unauthorized user"), "Unauthorized User", http.StatusForbidden)
				return
			}
		}
		for _, object := range envRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceEnvironment, casbin.ActionCreate, object); !ok {
				common.WriteJsonResp(w, fmt.Errorf("unauthorized user"), "Unauthorized User", http.StatusForbidden)
				return
			}
		}
	}
	//RBAC

	res, err := impl.notificationService.CreateOrUpdateNotificationSettings(&notificationSetting, userId)
	if err != nil {
		impl.logger.Errorw("service err, SaveNotificationSettings", "err", err, "payload", notificationSetting)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, nil, res, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) buildRbacObjectsForNotificationSettings(teamIds []*int, envIds []*int, appIds []*int, pipelineId *int, pipelineType util.PipelineType) ([]string, []string) {
	if teamIds == nil {
		teamIds = make([]*int, 0)
	}
	if envIds == nil {
		envIds = make([]*int, 0)
	}
	if appIds == nil {
		appIds = make([]*int, 0)
	}
	pid := 0
	if pipelineId == nil {
		pipelineId = &pid
	}
	var teamRbac []string
	var envRbac []string
	teamsMap := make(map[int]string)
	appsMap := make(map[int]string)
	if len(teamIds) > 0 && len(appIds) > 0 {
		teams, err := impl.teamService.FindByIds(teamIds)
		if err != nil {
		}
		apps, err := impl.pipelineBuilder.FindByIds(appIds)
		if err != nil {
		}
		for _, t := range teams {
			for _, a := range apps {
				teamRbac = append(teamRbac, fmt.Sprintf("%s/%s", strings.ToLower(t.Name), strings.ToLower(a.Name)))
				appsMap[a.Id] = a.Name
			}
		}

	} else if len(teamIds) == 0 && len(appIds) > 0 {
		apps, err := impl.pipelineBuilder.FindByIds(appIds)
		if err != nil {
		}
		for _, a := range apps {
			teamIds = append(teamIds, &a.TeamId)
			appsMap[a.Id] = a.Name
		}
		teams, err := impl.teamService.FindByIds(teamIds)
		if err != nil {
			impl.logger.Errorw("error", "error", err)
		}
		for _, t := range teams {
			for _, a := range apps {
				if t.Id == a.TeamId {
					teamRbac = append(teamRbac, fmt.Sprintf("%s/%s", strings.ToLower(t.Name), strings.ToLower(a.Name)))
				}
			}
		}
	} else if len(teamIds) > 0 && len(appIds) == 0 {
		teams, err := impl.teamService.FindByIds(teamIds)
		if err != nil {
		}
		for _, t := range teams {
			teamsMap[t.Id] = t.Name
			teamRbac = append(teamRbac, fmt.Sprintf("%s/*", strings.ToLower(t.Name)))
		}
	}
	if len(envIds) > 0 && len(appIds) == 0 {
		envs, err := impl.environmentService.FindByIds(envIds)
		if err != nil {
		}
		for _, e := range envs {
			envRbac = append(envRbac, fmt.Sprintf("%s/*", strings.ToLower(e.EnvironmentIdentifier)))
		}
	} else if len(envIds) > 0 && len(appIds) > 0 {
		envs, err := impl.environmentService.FindByIds(envIds)
		if err != nil {
		}
		for _, e := range envs {
			for _, aId := range appIds {
				envRbac = append(envRbac, fmt.Sprintf("%s/%s", strings.ToLower(e.EnvironmentIdentifier), appsMap[*aId]))
			}
		}
	}

	if *pipelineId > 0 {
		if pipelineType == util.CI {
			trbac := impl.enforcerUtil.GetTeamRbacObjectByCiPipelineId(*pipelineId)
			teamRbac = append(teamRbac, trbac)
		} else if pipelineType == util.CD {
			trbac, erbac := impl.enforcerUtil.GetTeamAndEnvironmentRbacObjectByCDPipelineId(*pipelineId)
			teamRbac = append(teamRbac, trbac)
			envRbac = append(envRbac, erbac)
		}
	}

	return teamRbac, envRbac
}

func (impl NotificationRestHandlerImpl) UpdateNotificationSettings(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	var notificationSetting notifier.NotificationUpdateRequest
	err = json.NewDecoder(r.Body).Decode(&notificationSetting)
	if err != nil {
		impl.logger.Errorw("request err, UpdateNotificationSettings", "err", err, "payload", notificationSetting)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	impl.logger.Infow("request payload, UpdateNotificationSettings", "err", err, "payload", notificationSetting)
	err = impl.validator.Struct(notificationSetting)
	if err != nil {
		impl.logger.Errorw("validation err, UpdateNotificationSettings", "err", err, "payload", notificationSetting)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	//RBAC
	token := r.Header.Get("token")
	var ids []*int
	for _, request := range notificationSetting.NotificationConfigRequest {
		ids = append(ids, &request.Id)
	}
	nsViews, err := impl.notificationService.FetchNSViewByIds(ids)
	if err != nil {
		impl.logger.Errorw("service err, UpdateNotificationSettings", "err", err, "payload", notificationSetting)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	for _, item := range nsViews {
		teamRbac, envRbac := impl.buildRbacObjectsForNotificationSettings(item.TeamId, item.EnvId, item.AppId, item.PipelineId, item.PipelineType)
		for _, object := range teamRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceApplications, casbin.ActionUpdate, object); !ok {
				common.WriteJsonResp(w, fmt.Errorf("unauthorized user"), "Unauthorized User", http.StatusForbidden)
				return
			}
		}
		for _, object := range envRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceEnvironment, casbin.ActionUpdate, object); !ok {
				common.WriteJsonResp(w, fmt.Errorf("unauthorized user"), "Unauthorized User", http.StatusForbidden)
				return
			}
		}
	}
	//RBAC

	res, err := impl.notificationService.UpdateNotificationSettings(&notificationSetting, userId)
	if err != nil {
		impl.logger.Errorw("service err, UpdateNotificationSettings", "err", err, "payload", notificationSetting)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, nil, res, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) DeleteNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var request notifier.NSDeleteRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		impl.logger.Errorw("request err, DeleteNotificationSettings", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	impl.logger.Infow("request payload, DeleteNotificationSettings", "err", err, "payload", request)
	token := r.Header.Get("token")
	//RBAC
	nsViews, err := impl.notificationService.FetchNSViewByIds(request.Id)
	if err != nil {
		impl.logger.Errorw("service err, DeleteNotificationSettings", "err", err, "payload", request)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	for _, item := range nsViews {
		teamRbac, envRbac := impl.buildRbacObjectsForNotificationSettings(item.TeamId, item.EnvId, item.AppId, item.PipelineId, item.PipelineType)
		for _, object := range teamRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceApplications, casbin.ActionDelete, object); !ok {
				common.WriteJsonResp(w, fmt.Errorf("unauthorized user"), "Unauthorized User", http.StatusForbidden)
				return
			}
		}
		for _, object := range envRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceEnvironment, casbin.ActionDelete, object); !ok {
				common.WriteJsonResp(w, fmt.Errorf("unauthorized user"), "Unauthorized User", http.StatusForbidden)
				return
			}
		}
	}
	//RBAC
	err = impl.notificationService.DeleteNotificationSettings(request)
	if err != nil {
		impl.logger.Errorw("service err, DeleteNotificationSettings", "err", err, "payload", request)
	}
	common.WriteJsonResp(w, err, nil, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) GetAllNotificationSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	size, err := strconv.Atoi(vars["size"])
	if err != nil {
		impl.logger.Errorw("request err, GetAllNotificationSettings", "err", err, "payload", size)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	offset, err := strconv.Atoi(vars["offset"])
	if err != nil {
		impl.logger.Errorw("request err, GetAllNotificationSettings", "err", err, "payload", offset)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	token := r.Header.Get("token")
	notificationSettingsViews, totalCount, err := impl.notificationService.FindAll(offset, size)
	if err != nil && err != pg.ErrNoRows {
		impl.logger.Errorw("service err, GetAllNotificationSettings", "err", err)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	var filteredSettingViews []*repository.NotificationSettingsViewWithAppEnv
	for _, ns := range notificationSettingsViews {
		nsConfig := &notifier.NSConfig{}
		err = json.Unmarshal([]byte(ns.Config), nsConfig)
		if err != nil {
			impl.logger.Errorw("service err, GetAllNotificationSettings", "err", err)
			common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
			return
		}
		teamRbac, envRbac := impl.buildRbacObjectsForNotificationSettings(nsConfig.TeamId, nsConfig.EnvId, nsConfig.AppId, nsConfig.PipelineId, nsConfig.PipelineType)
		pass := true
		for _, object := range teamRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceApplications, casbin.ActionGet, object); !ok {
				pass = false
				break
			}
		}
		if pass == false {
			continue
		}
		for _, object := range envRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceEnvironment, casbin.ActionGet, object); !ok {
				pass = false
				break
			}
		}
		if pass {
			filteredSettingViews = append(filteredSettingViews, ns)
		}
	}

	results, deletedItemCount, err := impl.notificationService.BuildNotificationSettingsResponse(filteredSettingViews)
	if err != nil {
		impl.logger.Errorw("service err, GetAllNotificationSettings", "err", err)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}
	totalCount = totalCount - deletedItemCount
	if results == nil {
		results = make([]*notifier.NotificationSettingsResponse, 0)
	}
	nsvResponse := notifier.NSViewResponse{
		Total:                        totalCount,
		NotificationSettingsResponse: results,
	}

	common.WriteJsonResp(w, err, nsvResponse, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) SaveNotificationChannelConfig(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}

	data, err := ioutil.ReadAll(r.Body)
	var channelReq ChannelDto
	err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&channelReq)
	if err != nil {
		impl.logger.Errorw("request err, SaveNotificationChannelConfig", "err", err, "payload", channelReq)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	impl.logger.Infow("request payload, SaveNotificationChannelConfig", "err", err, "payload", channelReq)
	token := r.Header.Get("token")
	if util.Slack == channelReq.Channel {
		var slackReq *notifier.SlackChannelConfig
		err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&slackReq)
		if err != nil {
			impl.logger.Errorw("request err, SaveNotificationChannelConfig", "err", err, "slackReq", slackReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		err = impl.validator.Struct(slackReq)
		if err != nil {
			impl.logger.Errorw("validation err, SaveNotificationChannelConfig", "err", err, "slackReq", slackReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		//RBAC
		var teamIds []*int
		for _, item := range slackReq.SlackConfigDtos {
			teamIds = append(teamIds, &item.TeamId)
		}
		teams, err := impl.teamService.FindByIds(teamIds)
		if err != nil {
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}
		for _, item := range teams {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceApplications, casbin.ActionCreate, fmt.Sprintf("%s/*", item.Name)); !ok {
				common.WriteJsonResp(w, err, "Unauthorized User", http.StatusForbidden)
				return
			}
		}
		//RBAC

		res, cErr := impl.slackService.SaveOrEditNotificationConfig(slackReq.SlackConfigDtos, userId)
		if cErr != nil {
			impl.logger.Errorw("service err, SaveNotificationChannelConfig", "err", err, "slackReq", slackReq)
			common.WriteJsonResp(w, cErr, nil, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		common.WriteJsonResp(w, nil, res, http.StatusOK)
	} else if util.SES == channelReq.Channel {
		var sesReq *notifier.SESChannelConfig
		err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&sesReq)
		if err != nil {
			impl.logger.Errorw("request err, SaveNotificationChannelConfig", "err", err, "sesReq", sesReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		err = impl.validator.Struct(sesReq)
		if err != nil {
			impl.logger.Errorw("validation err, SaveNotificationChannelConfig", "err", err, "sesReq", sesReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		// RBAC enforcer applying
		token := r.Header.Get("token")
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionCreate, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		//RBAC enforcer Ends

		res, cErr := impl.sesService.SaveOrEditNotificationConfig(sesReq.SESConfigDtos, userId)
		if cErr != nil {
			impl.logger.Errorw("service err, SaveNotificationChannelConfig", "err", err, "sesReq", sesReq)
			common.WriteJsonResp(w, cErr, nil, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		common.WriteJsonResp(w, nil, res, http.StatusOK)
	} else if util.SMTP == channelReq.Channel {
		var smtpReq *notifier.SMTPChannelConfig
		err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&smtpReq)
		if err != nil {
			impl.logger.Errorw("request err, SaveNotificationChannelConfig", "err", err, "smtpReq", smtpReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		err = impl.validator.Struct(smtpReq)
		if err != nil {
			impl.logger.Errorw("validation err, SaveNotificationChannelConfig", "err", err, "smtpReq", smtpReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		// RBAC enforcer applying
		token := r.Header.Get("token")
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionCreate, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		//RBAC enforcer Ends

		res, cErr := impl.smtpService.SaveOrEditNotificationConfig(smtpReq.SMTPConfigDtos, userId)
		if cErr != nil {
			impl.logger.Errorw("service err, SaveNotificationChannelConfig", "err", err, "smtpReq", smtpReq)
			common.WriteJsonResp(w, cErr, nil, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		common.WriteJsonResp(w, nil, res, http.StatusOK)
	} else if util.Webhook == channelReq.Channel {
		var webhookReq *notifier.WebhookChannelConfig
		err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&webhookReq)
		if err != nil {
			impl.logger.Errorw("request err, SaveNotificationChannelConfig", "err", err, "webhookReq", webhookReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		err = impl.validator.Struct(webhookReq)
		if err != nil {
			impl.logger.Errorw("validation err, SaveNotificationChannelConfig", "err", err, "webhookReq", webhookReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		// RBAC enforcer applying
		token := r.Header.Get("token")
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionCreate, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		//RBAC enforcer Ends

		res, cErr := impl.webhookService.SaveOrEditNotificationConfig(*webhookReq.WebhookConfigDtos, userId)
		if cErr != nil {
			impl.logger.Errorw("service err, SaveNotificationChannelConfig", "err", err, "webhookReq", webhookReq)
			common.WriteJsonResp(w, cErr, nil, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		common.WriteJsonResp(w, nil, res, http.StatusOK)
	}
}

type ChannelResponseDTO struct {
	SlackConfigs   []*notifier.SlackConfigDto   `json:"slackConfigs"`
	WebhookConfigs []*notifier.WebhookConfigDto `json:"webhookConfigs"`
	SESConfigs     []*notifier.SESConfigDto     `json:"sesConfigs"`
	SMTPConfigs    []*notifier.SMTPConfigDto    `json:"smtpConfigs"`
}

func (impl NotificationRestHandlerImpl) FindAllNotificationConfig(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}

	token := r.Header.Get("token")
	channelsResponse := &ChannelResponseDTO{}
	slackConfigs, fErr := impl.slackService.FetchAllSlackNotificationConfig()
	if fErr != nil && fErr != pg.ErrNoRows {
		impl.logger.Errorw("service err, FindAllNotificationConfig", "err", err)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}

	if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionGet, "*"); !ok {
		// if user does not have notification level access then return unauthorized
		common.WriteJsonResp(w, fmt.Errorf("unauthorized user"), nil, http.StatusForbidden)
		return
	}
	//RBAC
	pass := true
	if len(slackConfigs) > 0 {
		var teamIds []*int
		for _, item := range slackConfigs {
			teamIds = append(teamIds, &item.TeamId)
		}
		teams, err := impl.teamService.FindByIds(teamIds)
		if err != nil {
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}
		for _, item := range teams {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceApplications, casbin.ActionGet, fmt.Sprintf("%s/*", item.Name)); !ok {
				pass = false
				break
			}
		}
	}
	//RBAC
	if slackConfigs == nil {
		slackConfigs = make([]*notifier.SlackConfigDto, 0)
	}
	if pass {
		channelsResponse.SlackConfigs = slackConfigs
	}
	webhookConfigs, fErr := impl.webhookService.FetchAllWebhookNotificationConfig()
	if fErr != nil && fErr != pg.ErrNoRows {
		impl.logger.Errorw("service err, FindAllNotificationConfig", "err", err)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}
	if webhookConfigs == nil {
		webhookConfigs = make([]*notifier.WebhookConfigDto, 0)
	}
	if pass {
		channelsResponse.WebhookConfigs = webhookConfigs
	}
	sesConfigs, fErr := impl.sesService.FetchAllSESNotificationConfig()
	if fErr != nil && fErr != pg.ErrNoRows {
		impl.logger.Errorw("service err, FindAllNotificationConfig", "err", err)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}
	if sesConfigs == nil {
		sesConfigs = make([]*notifier.SESConfigDto, 0)
	}
	if pass {
		channelsResponse.SESConfigs = sesConfigs
	}

	smtpConfigs, err := impl.smtpService.FetchAllSMTPNotificationConfig()
	if err != nil && err != pg.ErrNoRows {
		impl.logger.Errorw("service err, FindAllNotificationConfig", "err", err)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}
	if smtpConfigs == nil {
		smtpConfigs = make([]*notifier.SMTPConfigDto, 0)
	}
	if pass {
		channelsResponse.SMTPConfigs = smtpConfigs
	}
	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, fErr, channelsResponse, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) FindSESConfig(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		impl.logger.Errorw("request err, FindSESConfig", "err", err)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	token := r.Header.Get("token")
	if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionGet, "*"); !ok {
		response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
	}

	sesConfig, fErr := impl.sesService.FetchSESNotificationConfigById(id)
	if fErr != nil && fErr != pg.ErrNoRows {
		impl.logger.Errorw("service err, FindSESConfig", "err", err)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, fErr, sesConfig, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) FindSlackConfig(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		impl.logger.Errorw("request err, FindSlackConfig", "err", err, "id", id)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	sesConfig, fErr := impl.slackService.FetchSlackNotificationConfigById(id)
	if fErr != nil && fErr != pg.ErrNoRows {
		impl.logger.Errorw("service err, FindSlackConfig, cannot find slack config", "err", fErr, "id", id)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, fErr, sesConfig, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) FindSMTPConfig(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		impl.logger.Errorw("request err, FindSMTPConfig", "err", err)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	token := r.Header.Get("token")
	if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionGet, "*"); !ok {
		response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
	}

	smtpConfig, fErr := impl.smtpService.FetchSMTPNotificationConfigById(id)
	if fErr != nil && fErr != pg.ErrNoRows {
		impl.logger.Errorw("service err, FindSMTPConfig", "err", err)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, fErr, smtpConfig, http.StatusOK)
}
func (impl NotificationRestHandlerImpl) FindWebhookConfig(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	vars := mux.Vars(r)
	id, err := strconv.Atoi(vars["id"])
	if err != nil {
		impl.logger.Errorw("request err, FindWebhookConfig", "err", err, "id", id)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}

	webhookConfig, fErr := impl.webhookService.FetchWebhookNotificationConfigById(id)
	if fErr != nil && fErr != pg.ErrNoRows {
		impl.logger.Errorw("service err, FindWebhookConfig, cannot find webhook config", "err", fErr, "id", id)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, fErr, webhookConfig, http.StatusOK)
}
func (impl NotificationRestHandlerImpl) GetWebhookVariables(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}

	webhookVariables, fErr := impl.webhookService.GetWebhookVariables()
	if fErr != nil && fErr != pg.ErrNoRows {
		impl.logger.Errorw("service err, GetWebhookVariables, cannot find webhook Variables", "err", fErr)
		common.WriteJsonResp(w, fErr, nil, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, fErr, webhookVariables, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) RecipientListingSuggestion(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	token := r.Header.Get("token")
	if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionGet, "*"); !ok {
		common.WriteJsonResp(w, errors.New("unauthorized"), "Forbidden", http.StatusForbidden)
		return
	}
	vars := mux.Vars(r)
	value := vars["value"]
	//var teams []int
	var channelsResponse []*notifier.NotificationRecipientListingResponse
	channelsResponse, err = impl.slackService.RecipientListingSuggestion(value)
	if err != nil {
		impl.logger.Errorw("service err, RecipientListingSuggestion", "err", err, "value", value)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}

	if channelsResponse == nil {
		channelsResponse = make([]*notifier.NotificationRecipientListingResponse, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, nil, channelsResponse, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) FindAllNotificationConfigAutocomplete(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}

	// RBAC enforcer applying
	token := r.Header.Get("token")
	if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionGet, "*"); !ok {
		response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
		return
	}
	//RBAC enforcer Ends
	vars := mux.Vars(r)
	cType := vars["type"]
	var channelsResponse []*notifier.NotificationChannelAutoResponse
	if cType == string(util.Slack) {
		channelsResponseAll, err := impl.slackService.FetchAllSlackNotificationConfigAutocomplete()
		if err != nil && err != pg.ErrNoRows {
			impl.logger.Errorw("service err, FindAllNotificationConfigAutocomplete", "err", err)
			common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
			return
		}
		for _, item := range channelsResponseAll {
			team, err := impl.teamService.FetchOne(item.TeamId)
			if err != nil {
				common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
				return
			}
			if ok := impl.enforcer.Enforce(token, casbin.ResourceApplications, casbin.ActionGet, fmt.Sprintf("%s/*", team.Name)); ok {
				channelsResponse = append(channelsResponse, item)
			}
		}

	} else if cType == string(util.Webhook) {
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionGet, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		channelsResponse, err = impl.webhookService.FetchAllWebhookNotificationConfigAutocomplete()
		if err != nil && err != pg.ErrNoRows {
			impl.logger.Errorw("service err, FindAllNotificationConfigAutocomplete", "err", err)
			common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
			return
		}
	} else if cType == string(util.SES) {
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionGet, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		channelsResponse, err = impl.sesService.FetchAllSESNotificationConfigAutocomplete()
		if err != nil && err != pg.ErrNoRows {
			impl.logger.Errorw("service err, FindAllNotificationConfigAutocomplete", "err", err)
			common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
			return
		}
	} else if cType == string(util.SMTP) {
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionGet, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		channelsResponse, err = impl.smtpService.FetchAllSMTPNotificationConfigAutocomplete()
		if err != nil && err != pg.ErrNoRows {
			impl.logger.Errorw("service err, FindAllNotificationConfigAutocomplete", "err", err)
			common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
			return
		}
	}
	if channelsResponse == nil {
		channelsResponse = make([]*notifier.NotificationChannelAutoResponse, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	common.WriteJsonResp(w, nil, channelsResponse, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) GetOptionsForNotificationSettings(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	var request repository.SearchRequest
	err = decoder.Decode(&request)
	if err != nil {
		impl.logger.Errorw("request err, GetOptionsForNotificationSettings", "err", err, "request", request)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	request.UserId = userId

	notificationSettingsOptions, err := impl.notificationService.FindNotificationSettingOptions(&request)
	if err != nil && err != pg.ErrNoRows {
		impl.logger.Errorw("service err, GetOptionsForNotificationSettings", "err", err)
		common.WriteJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}

	// RBAC enforcer applying
	token := r.Header.Get("token")
	var filteredSettingViews []*notifier.SearchFilterResponse
	for _, ns := range notificationSettingsOptions {
		teamIds := make([]*int, 0)
		envIds := make([]*int, 0)
		appIds := make([]*int, 0)
		for _, item := range ns.TeamResponse {
			teamIds = append(teamIds, item.Id)
		}
		for _, item := range ns.EnvResponse {
			envIds = append(envIds, item.Id)
		}
		for _, item := range ns.AppResponse {
			appIds = append(appIds, item.Id)
		}
		pId := 0
		if ns.PipelineResponse != nil && *ns.PipelineResponse.Id > 0 {
			pId = *ns.PipelineResponse.Id
		}
		teamRbac, envRbac := impl.buildRbacObjectsForNotificationSettings(teamIds, envIds, appIds, &pId, util.PipelineType(ns.PipelineType))
		pass := false

		for _, object := range teamRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceApplications, casbin.ActionCreate, object); ok {
				pass = true
			}
		}
		for _, object := range envRbac {
			if ok := impl.enforcer.Enforce(token, casbin.ResourceEnvironment, casbin.ActionCreate, object); ok {
				pass = true
			}
		}
		if pass {
			filteredSettingViews = append(filteredSettingViews, ns)
		}
	}
	//RBAC

	if filteredSettingViews == nil {
		filteredSettingViews = make([]*notifier.SearchFilterResponse, 0)
	}
	common.WriteJsonResp(w, err, filteredSettingViews, http.StatusOK)
}

func (impl NotificationRestHandlerImpl) DeleteNotificationChannelConfig(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		common.WriteJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}

	data, err := ioutil.ReadAll(r.Body)
	var channelReq ChannelDto
	err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&channelReq)
	if err != nil {
		impl.logger.Errorw("request err, DeleteNotificationChannelConfig", "err", err, "payload", channelReq)
		common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
		return
	}
	impl.logger.Infow("request payload, DeleteNotificationChannelConfig", "err", err, "payload", channelReq)
	if util.Slack == channelReq.Channel {
		var deleteReq *notifier.SlackConfigDto
		err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&deleteReq)
		if err != nil {
			impl.logger.Errorw("request err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		err = impl.validator.Struct(deleteReq)
		if err != nil {
			impl.logger.Errorw("validation err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		// RBAC enforcer applying
		token := r.Header.Get("token")
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionCreate, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		//RBAC enforcer Ends

		cErr := impl.slackService.DeleteNotificationConfig(deleteReq, userId)
		if cErr != nil {
			impl.logger.Errorw("service err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, cErr, nil, http.StatusInternalServerError)
			return
		}
		common.WriteJsonResp(w, nil, SLACK_CONFIG_DELETE_SUCCESS_RESP, http.StatusOK)
	} else if util.Webhook == channelReq.Channel {
		var deleteReq *notifier.WebhookConfigDto
		err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&deleteReq)
		if err != nil {
			impl.logger.Errorw("request err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		err = impl.validator.Struct(deleteReq)
		if err != nil {
			impl.logger.Errorw("validation err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		// RBAC enforcer applying
		token := r.Header.Get("token")
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionCreate, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		//RBAC enforcer Ends

		cErr := impl.webhookService.DeleteNotificationConfig(deleteReq, userId)
		if cErr != nil {
			impl.logger.Errorw("service err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, cErr, nil, http.StatusInternalServerError)
			return
		}
		common.WriteJsonResp(w, nil, WEBHOOK_CONFIG_DELETE_SUCCESS_RESP, http.StatusOK)
	} else if util.SES == channelReq.Channel {
		var deleteReq *notifier.SESConfigDto
		err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&deleteReq)
		if err != nil {
			impl.logger.Errorw("request err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		err = impl.validator.Struct(deleteReq)
		if err != nil {
			impl.logger.Errorw("validation err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		// RBAC enforcer applying
		token := r.Header.Get("token")
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionCreate, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		//RBAC enforcer Ends

		cErr := impl.sesService.DeleteNotificationConfig(deleteReq, userId)
		if cErr != nil {
			impl.logger.Errorw("service err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, cErr, nil, http.StatusInternalServerError)
			return
		}
		common.WriteJsonResp(w, nil, SES_CONFIG_DELETE_SUCCESS_RESP, http.StatusOK)
	} else if util.SMTP == channelReq.Channel {
		var deleteReq *notifier.SMTPConfigDto
		err = json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(data))).Decode(&deleteReq)
		if err != nil {
			impl.logger.Errorw("request err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		err = impl.validator.Struct(deleteReq)
		if err != nil {
			impl.logger.Errorw("validation err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, err, nil, http.StatusBadRequest)
			return
		}

		// RBAC enforcer applying
		token := r.Header.Get("token")
		if ok := impl.enforcer.Enforce(token, casbin.ResourceNotification, casbin.ActionCreate, "*"); !ok {
			response.WriteResponse(http.StatusForbidden, "FORBIDDEN", w, errors.New("unauthorized"))
			return
		}
		//RBAC enforcer Ends

		cErr := impl.smtpService.DeleteNotificationConfig(deleteReq, userId)
		if cErr != nil {
			impl.logger.Errorw("service err, DeleteNotificationChannelConfig", "err", err, "deleteReq", deleteReq)
			common.WriteJsonResp(w, cErr, nil, http.StatusInternalServerError)
			return
		}
		common.WriteJsonResp(w, nil, SMTP_CONFIG_DELETE_SUCCESS_RESP, http.StatusOK)
	} else {
		common.WriteJsonResp(w, fmt.Errorf(" The channel you requested is not supported"), nil, http.StatusBadRequest)
	}
}
