package profile

import (
	"github.com/itchio/butler/butlerd"
	"github.com/itchio/butler/butlerd/messages"
	"github.com/itchio/butler/database/models"
	"github.com/itchio/go-itchio"
	"github.com/pkg/errors"
)

func Register(router *butlerd.Router) {
	messages.ProfileList.Register(router, List)
	messages.ProfileLoginWithPassword.Register(router, LoginWithPassword)
	messages.ProfileLoginWithAPIKey.Register(router, LoginWithAPIKey)
	messages.ProfileUseSavedLogin.Register(router, UseSavedLogin)
	messages.ProfileForget.Register(router, Forget)
	messages.ProfileDataPut.Register(router, DataPut)
	messages.ProfileDataGet.Register(router, DataGet)
}

func List(rc *butlerd.RequestContext, params *butlerd.ProfileListParams) (*butlerd.ProfileListResult, error) {
	var profiles []*models.Profile
	err := rc.DB().Preload("User").Order("last_connected desc").Find(&profiles).Error
	if err != nil {
		return nil, errors.WithStack(err)
	}

	var formattedProfiles []*butlerd.Profile
	for _, profile := range profiles {
		formattedProfiles = append(formattedProfiles, formatProfile(profile))
	}

	return &butlerd.ProfileListResult{
		Profiles: formattedProfiles,
	}, nil
}

func formatProfile(p *models.Profile) *butlerd.Profile {
	return &butlerd.Profile{
		ID:            p.ID,
		LastConnected: p.LastConnected,
		User:          p.User,
	}
}

func LoginWithPassword(rc *butlerd.RequestContext, params *butlerd.ProfileLoginWithPasswordParams) (*butlerd.ProfileLoginWithPasswordResult, error) {
	if params.Username == "" {
		return nil, errors.New("username cannot be empty")
	}
	if params.Password == "" {
		return nil, errors.New("password cannot be empty")
	}

	rootClient, err := rc.RootClient()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	var key *itchio.APIKey
	var cookie itchio.Cookie

	{
		loginRes, err := rootClient.LoginWithPassword(&itchio.LoginWithPasswordParams{
			Username: params.Username,
			Password: params.Password,
		})
		if err != nil {
			return nil, errors.WithStack(err)
		}

		if loginRes.RecaptchaNeeded {
			// Captcha flow
			recaptchaRes, err := messages.ProfileRequestCaptcha.Call(rc, &butlerd.ProfileRequestCaptchaParams{
				RecaptchaURL: loginRes.RecaptchaURL,
			})
			if err != nil {
				return nil, errors.WithStack(err)
			}

			if recaptchaRes.RecaptchaResponse == "" {
				return nil, &butlerd.ErrAborted{}
			}

			loginRes, err = rootClient.LoginWithPassword(&itchio.LoginWithPasswordParams{
				Username:          params.Username,
				Password:          params.Password,
				RecaptchaResponse: recaptchaRes.RecaptchaResponse,
			})
			if err != nil {
				return nil, errors.WithStack(err)
			}
		}

		if loginRes.Token != "" {
			// TOTP flow
			totpRes, err := messages.ProfileRequestTOTP.Call(rc, &butlerd.ProfileRequestTOTPParams{})
			if err != nil {
				return nil, errors.WithStack(err)
			}

			if totpRes.Code == "" {
				return nil, &butlerd.ErrAborted{}
			}

			verifyRes, err := rootClient.TOTPVerify(&itchio.TOTPVerifyParams{
				Token: loginRes.Token,
				Code:  totpRes.Code,
			})
			if err != nil {
				return nil, errors.WithStack(err)
			}

			key = verifyRes.Key
			cookie = verifyRes.Cookie
		} else {
			// One-factor flow
			key = loginRes.Key
			cookie = loginRes.Cookie
		}
	}

	client, err := rc.KeyClient(key.Key)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	meRes, err := client.GetMe()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	profile := &models.Profile{
		ID:     meRes.User.ID,
		APIKey: key.Key,
	}
	profile.UpdateFromUser(meRes.User)

	err = rc.DB().Save(profile).Error
	if err != nil {
		return nil, errors.WithStack(err)
	}

	res := &butlerd.ProfileLoginWithPasswordResult{
		Cookie:  cookie,
		Profile: formatProfile(profile),
	}
	return res, nil
}

func LoginWithAPIKey(rc *butlerd.RequestContext, params *butlerd.ProfileLoginWithAPIKeyParams) (*butlerd.ProfileLoginWithAPIKeyResult, error) {
	if params.APIKey == "" {
		return nil, errors.New("apiKey cannot be empty")
	}

	client, err := rc.KeyClient(params.APIKey)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	meRes, err := client.GetMe()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	profile := &models.Profile{
		ID:     meRes.User.ID,
		APIKey: params.APIKey,
	}
	profile.UpdateFromUser(meRes.User)

	err = rc.DB().Save(profile).Error
	if err != nil {
		return nil, errors.WithStack(err)
	}

	res := &butlerd.ProfileLoginWithAPIKeyResult{
		Profile: formatProfile(profile),
	}
	return res, nil
}

func UseSavedLogin(rc *butlerd.RequestContext, params *butlerd.ProfileUseSavedLoginParams) (*butlerd.ProfileUseSavedLoginResult, error) {
	consumer := rc.Consumer

	profile, client := rc.ProfileClient(params.ProfileID)

	consumer.Opf("Validating credentials...")

	meRes, err := client.GetMe()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	profile.UpdateFromUser(meRes.User)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	err = rc.DB().Save(profile).Error
	if err != nil {
		return nil, errors.WithStack(err)
	}

	consumer.Opf("Logged in!")

	res := &butlerd.ProfileUseSavedLoginResult{
		Profile: formatProfile(profile),
	}
	return res, nil
}

func Forget(rc *butlerd.RequestContext, params *butlerd.ProfileForgetParams) (*butlerd.ProfileForgetResult, error) {
	if params.ProfileID == 0 {
		return nil, errors.New("profileId must be set")
	}

	success := false

	profile := models.ProfileByID(rc.DB(), params.ProfileID)
	if profile != nil {
		err := rc.DB().Delete(profile).Error
		if err != nil {
			return nil, errors.WithStack(err)
		}
		success = true
	}

	res := &butlerd.ProfileForgetResult{
		Success: success,
	}
	return res, nil
}
