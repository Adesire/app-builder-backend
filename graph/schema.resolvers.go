package graph

// This file will be automatically regenerated based on the schema, any resolver implementations
// will be copied through when generating and any unknown code will be moved to the end.

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/samyak-jain/agora_backend/graph/generated"
	"github.com/samyak-jain/agora_backend/graph/model"
	"github.com/samyak-jain/agora_backend/pkg/video_conferencing/middleware"
	"github.com/samyak-jain/agora_backend/pkg/video_conferencing/models"

	"github.com/samyak-jain/agora_backend/utils"
	"github.com/spf13/viper"
)

var errInternalServer error = errors.New("Internal Server Error")
var errBadRequest error = errors.New("Bad Request")

func (r *mutationResolver) CreateChannel(ctx context.Context, title string, enablePstn *bool) (*model.ShareResponse, error) {
	r.Logger.Info().Str("mutation", "CreateChannel").Str("title", title).Msg("Creating Channel")
	if enablePstn != nil {
		r.Logger.Info().Bool("enablePstn", *enablePstn).Msg("")
	}

	if viper.GetBool("ENABLE_OAUTH") {
		_, err := middleware.GetUserFromContext(ctx)
		if err != nil {
			r.Logger.Debug().Msg("Invalid Token")
			return nil, errors.New("Invalid Token")
		}
	}

	var pstnResponse *model.Pstn
	var newChannel *models.Channel

	hostPhrase, err := utils.GenerateUUID()
	if err != nil {
		r.Logger.Error().Err(err).Msg("Host Phrase generation failed")
		return nil, errInternalServer
	}

	viewPhrase, err := utils.GenerateUUID()
	if err != nil {
		r.Logger.Error().Err(err).Msg("View Phrase generation failed")
		return nil, errInternalServer
	}

	channelName, err := utils.GenerateUUID()
	if err != nil {
		r.Logger.Error().Err(err).Msg("Channel Name generation failed")
		return nil, errInternalServer
	}

	channel := strings.ReplaceAll(channelName, "-", "")

	secretGen, err := utils.GenerateUUID()
	if err != nil {
		return nil, err
	}
	secret := strings.ReplaceAll(secretGen, "-", "")
	dtmfResult, err := utils.GenerateDTMF()
	if err != nil {
		r.Logger.Error().Err(err).Msg("DTMF generation failed")
		return nil, errInternalServer
	}

	if *enablePstn {
		pstnResponse = &model.Pstn{
			Number: viper.GetString("PSTN_NUMBER"),
			Dtmf:   *dtmfResult,
		}
	} else {
		pstnResponse = nil
	}

	newChannel = &models.Channel{
		Title:            title,
		ChannelName:      channel,
		ChannelSecret:    secret,
		HostPassphrase:   hostPhrase,
		ViewerPassphrase: viewPhrase,
		DTMF:             *dtmfResult,
	}

	_, err = r.DB.NamedExec("INSERT INTO channels (title, channel_name, channel_secret, host_passphrase, viewer_passphrase, dtmf) VALUES (:title, :channel_name, :channel_secret, :host_passphrase, :viewer_passphrase, :dtmf)", newChannel)

	if err != nil {
		r.Logger.Error().Err(err).Interface("channel details", newChannel).Msg("Adding new channel to DB Failed")
		return nil, errInternalServer
	}

	return &model.ShareResponse{
		Passphrase: &model.Passphrase{
			Host: &hostPhrase,
			View: viewPhrase,
		},
		Title:   title,
		Channel: channel,
		Pstn:    pstnResponse,
	}, nil
}

func (r *mutationResolver) UpdateUserName(ctx context.Context, name string) (*model.User, error) {
	r.Logger.Info().Str("mutation", "UpdateUserName").Str("name", name).Msg("")

	if !viper.GetBool("ENABLE_OAUTH") {
		return nil, nil
	}

	authUser, err := middleware.GetUserFromContext(ctx)
	if err != nil {
		r.Logger.Debug().Msg("Invalid Token")
		return nil, errors.New("Invalid Token")
	}

	_, err = r.DB.NamedExec("UPDATE users SET user_name = ':name' WHERE identifier = ':ident'", &models.User{
		Identifier: authUser.Identifier,
		UserName: sql.NullString{
			String: name,
			Valid:  true,
		},
	})

	if err != nil {
		r.Logger.Error().Err(err).Str("identifier", authUser.Identifier).Msg("Username update failed")
		return nil, errInternalServer
	}

	return &model.User{
		Name: name,
	}, nil
}

func (r *mutationResolver) StartRecordingSession(ctx context.Context, passphrase string, secret *string) (string, error) {
	r.Logger.Info().Str("mutation", "StartRecordingSession").Str("passphrase", passphrase).Msg("")
	if secret != nil {
		r.Logger.Info().Str("secret", *secret).Msg("")
	}

	var channelData models.Channel
	var host bool

	var authUser *models.User
	if viper.GetBool("ENABLE_OAUTH") {
		authUser = middleware.GetUserFromContext(ctx)
	}

	if passphrase == "" {
		return "", errors.New("Passphrase cannot be empty")
	}

	err := r.DB.Get(&channelData, "SELECT id, title, channel_name, channel_secret, host_passphrase, viewer_passphrase FROM channels WHERE host_passphrase = $1 OR viewer_passphrase = $1", passphrase)
	if err != nil {
		r.Logger.Debug().Str("passphrase", passphrase).Msg("Invalid Passphrase")
		return "", errors.New("Invalid URL")
	}

	if passphrase == channelData.HostPassphrase {
		host = true
	} else if passphrase == channelData.ViewerPassphrase {
		host = false
	} else {
		r.Logger.Debug().Str("passphrase", passphrase).Msg("Invalid Passphrase; Interal Server Error")
		return "", errors.New("Invalid URL")
	}

	if !host {
		r.Logger.Debug().Str("passphrase", passphrase).Str("channel", channelData.ChannelName).Msg("Unauthorized to record channel")
		return "", errors.New("Unauthorised to record channel")
	}

	var title string
	if authUser == nil {
		title = channelData.Title
	} else {
		title = authUser.Name
	}

	reg, err := regexp.Compile("[^a-zA-Z0-9]+")
	if err != nil {
		log.Error().Err(err).Msg("Regex Compilation failed")
	}

	finalTitle := utils.FirstN(reg.ReplaceAllString(title, ""), 100)

	recorder := &utils.Recorder{}
	recorder.Channel = channelData.ChannelName

	err = recorder.Acquire()
	if err != nil {
		r.Logger.Error().Err(err).Msg("Acquire Failed")
		return "", errInternalServer
	}

	err = recorder.Start(finalTitle, secret)
	if err != nil {
		r.Logger.Error().Err(err).Msg("Start Failed")
		return "", errInternalServer
	}
	recordDetails := models.Channel{
		ID:           channelData.ID,
		RecordingUID: sql.NullInt32{Int32: recorder.UID, Valid: true},
		RecordingRID: sql.NullString{String: recorder.RID, Valid: true},
		RecordingSID: sql.NullString{String: recorder.SID, Valid: true},
	}

	_, err = r.DB.NamedExec("UPDATE channels SET (recording_uid, recording_sid, recording_rid) = (:recording_uid, :recording_sid, :recording_rid) WHERE id = :id", &recordDetails)
	if err != nil {
		r.Logger.Error().Err(err).Msg("Updating database for recording failed")
		return "", errInternalServer
	}

	return "success", nil
}

func (r *mutationResolver) StopRecordingSession(ctx context.Context, passphrase string) (string, error) {
	r.Logger.Info().Str("mutation", "StopRecordingSession").Str("passphrase", passphrase).Msg("")

	var channelData models.Channel
	var host bool

	if passphrase == "" {
		return "", errors.New("Passphrase cannot be empty")
	}

	err := r.DB.Get(&channelData, "SELECT id, title, channel_name, channel_secret, host_passphrase, viewer_passphrase FROM channels WHERE host_passphrase = $1 OR viewer_passphrase = $1", passphrase)
	if err != nil {
		r.Logger.Debug().Str("passphrase", passphrase).Msg("Invalid Passphrase")
		return "", errors.New("Invalid URL")
	}

	if passphrase == channelData.HostPassphrase {
		host = true
	} else if passphrase == channelData.ViewerPassphrase {
		host = false
	} else {
		r.Logger.Debug().Str("passphrase", passphrase).Msg("Invalid Passphrase; Interal Server Error")
		return "", errors.New("Invalid URL")
	}
	if !host {
		r.Logger.Debug().Str("passphrase", passphrase).Str("channel", channelData.ChannelName).Msg("Unauthorized to record channel")
		return "", errors.New("Unauthorised to record channel")
	}

	if !channelData.RecordingRID.Valid || !channelData.RecordingSID.Valid || !channelData.RecordingUID.Valid {
		r.Logger.Debug().Interface("Channel Data", channelData).Msg("RID or SID or UID not in DB")
		return "", errors.New("Recording not started")
	}

	err = utils.Stop(channelData.ChannelName, int(channelData.RecordingUID.Int32), channelData.RecordingRID.String, channelData.RecordingSID.String)
	if err != nil {
		r.Logger.Error().Err(err).Msg("Stop recording failed")
		return "", errInternalServer
	}

	return "success", nil
}

func (r *mutationResolver) LogoutSession(ctx context.Context, token string) ([]string, error) {
	r.Logger.Info().Str("mutation", "LogoutSession").Str("token", token).Msg("")

	authUser, err := middleware.GetUserFromContext(ctx)
	if err != nil {
		r.Logger.Debug().Msg("Invalid Token")
		return nil, errors.New("Invalid Token")
	}

	res, err := r.DB.NamedExec("DELETE FROM tokens WHERE token_id = :token_id AND user_id = :user_id", &models.Token{
		TokenID: token,
		UserID:  authUser.ID,
	})

	if err != nil {
		r.Logger.Error().Err(err).Msg("Could not delete token from database")
		return nil, errInternalServer
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		r.Logger.Error().Str("Token", token).Int64("User ID", authUser.ID).Msg("Could not get Rows Affected by DELETE in database")
		return nil, errInternalServer
	}

	if rowsAffected < 1 {
		r.Logger.Debug().Str("Sub", authUser.Identifier).Msg("Token does not exist")
		return nil, errBadRequest
	}

	tokens := []models.Token{}
	string_token_slice := []string{}
	err = r.DB.Select(&tokens, "SELECT * FROM tokens WHERE user_id = $1", authUser.ID)
	if err != nil {
		r.Logger.Error().Err(err).Int64("User ID", authUser.ID).Msg("Could not get tokens for this user ID")
		return nil, errInternalServer
	}

	for _, v := range tokens {
		string_token_slice = append(string_token_slice, v.TokenID)
	}

	return string_token_slice, nil
}

func (r *mutationResolver) LogoutAllSessions(ctx context.Context) (*string, error) {
	r.Logger.Info().Str("mutation", "LogoutAllSessions").Msg("")

	// authUser, err := middleware.GetUserFromContext(ctx)
	// if err != nil {
	// 	r.Logger.Debug().Msg("Invalid Token")
	// 	return nil, errors.New("Invalid Token")
	// }

	// TODO: Switch to SQLX

	// if err := r.DB.Where("identifier = ?", authUser.ID).Delete(models.Token{}).Error; err != nil {
	// 	r.Logger.Error().Err(err).Msg("Could not delete all the tokens from the database")
	// 	return nil, errInternalServer
	// }

	return nil, nil
}

func (r *queryResolver) JoinChannel(ctx context.Context, passphrase string) (*model.Session, error) {
	r.Logger.Info().Str("query", "JoinChannel").Str("passphrase", passphrase).Msg("")

	var channelData models.Channel
	var host bool

	if passphrase == "" {
		return nil, errors.New("Passphrase cannot be empty")
	}

	err := r.DB.Get(&channelData, "SELECT title, channel_name, channel_secret, host_passphrase, viewer_passphrase FROM channels WHERE host_passphrase = $1 OR viewer_passphrase = $1", passphrase)
	if err != nil {
		r.Logger.Debug().Str("passphrase", passphrase).Msg("Invalid Passphrase")
		return nil, errors.New("Invalid URL")
	}

	if passphrase == channelData.HostPassphrase {
		host = true
	} else if passphrase == channelData.ViewerPassphrase {
		host = false
	} else {
		r.Logger.Debug().Str("passphrase", passphrase).Msg("Invalid Passphrase; Interal Server Error")
		return nil, errors.New("Invalid URL")
	}

	mainUser, err := utils.GenerateUserCredentials(channelData.ChannelName, true)
	if err != nil {
		r.Logger.Error().Err(err).Msg("Could not generate main user credentials")
		return nil, errInternalServer
	}

	screenShare, err := utils.GenerateUserCredentials(channelData.ChannelName, false)
	if err != nil {
		r.Logger.Error().Err(err).Msg("Could not generate screenshare user credentails")
		return nil, errInternalServer
	}

	return &model.Session{
		Title:       channelData.Title,
		Channel:     channelData.ChannelName,
		IsHost:      host,
		MainUser:    mainUser,
		ScreenShare: screenShare,
		Secret:      channelData.ChannelSecret,
	}, nil
}

func (r *queryResolver) Share(ctx context.Context, passphrase string) (*model.ShareResponse, error) {
	r.Logger.Info().Str("query", "Share").Str("passphrase", passphrase).Msg("")

	var channelData models.Channel
	var host bool

	if passphrase == "" {
		return nil, errors.New("Passphrase cannot be empty")
	}

	err := r.DB.Get(&channelData, "SELECT title, channel_name, channel_secret, host_passphrase, viewer_passphrase FROM channels WHERE host_passphrase = '$1' OR viewer_passphrase = '$1'", passphrase)
	if err != nil {
		r.Logger.Debug().Str("passphrase", passphrase).Msg("Invalid Passphrase")
		return nil, errors.New("Invalid URL")
	}

	if passphrase == channelData.HostPassphrase {
		host = true
	} else if passphrase == channelData.ViewerPassphrase {
		host = false
	} else {
		r.Logger.Debug().Str("passphrase", passphrase).Msg("Invalid Passphrase; Interal Server Error")
		return nil, errors.New("Invalid URL")
	}

	var hostPassphrase *string
	if host {
		hostPassphrase = &channelData.HostPassphrase
	} else {
		hostPassphrase = nil
	}

	var pstnResult *model.Pstn
	if channelData.DTMF != "" {
		pstnResult = &model.Pstn{
			Number: viper.GetString("PSTN_NUMBER"),
			Dtmf:   channelData.DTMF,
		}
	} else {
		pstnResult = nil
	}

	return &model.ShareResponse{
		Passphrase: &model.Passphrase{
			Host: hostPassphrase,
			View: channelData.ViewerPassphrase,
		},
		Channel: channelData.ChannelName,
		Title:   channelData.Title,
		Pstn:    pstnResult,
	}, nil
}

func (r *queryResolver) GetUser(ctx context.Context) (*model.User, error) {
	r.Logger.Info().Str("query", "GetUser").Msg("")

	if !viper.GetBool("ENABLE_OAUTH") {
		return nil, nil
	}

	authUser, err := middleware.GetUserFromContext(ctx)
	if err != nil {
		r.Logger.Debug().Msg("Invalid Token")
		return nil, errors.New("Invalid Token")
	}

	if !authUser.UserName.Valid {
		return &model.User{
			Name: "",
		}, nil
	}

	return &model.User{
		Name: authUser.UserName.String,
	}, nil
}

func (r *queryResolver) GetSessions(ctx context.Context) ([]string, error) {
	r.Logger.Info().Str("query", "GetSessions").Msg("")

	_, err := middleware.GetUserFromContext(ctx)
	if err != nil {
		r.Logger.Debug().Msg("Invalid Token")
		return nil, errors.New("Invalid Token")
	}

	// TODO: Switch to SQLX

	// if err := r.DB.Preload("Tokens").Find(&authUser).Error; err != nil {
	// 	r.Logger.Error().Err(err).Msg("Could not preload all the tokens")
	// 	return nil, errInternalServer
	// }

	// return authUser.GetAllTokens(), nil
	return []string{""}, nil
}

// Mutation returns generated.MutationResolver implementation.
func (r *Resolver) Mutation() generated.MutationResolver { return &mutationResolver{r} }

// Query returns generated.QueryResolver implementation.
func (r *Resolver) Query() generated.QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
