package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/mailgun/mailgun-go/v4"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"zuri.chat/zccore/utils"
)

type MailService interface {
	LoadTemplate(mailReq *Mail) ([]byte, error)
	SendMail(mailReq *Mail) error
	NewMail(to []string, subject string, mailType MailType, data *MailData) *Mail
}

type MailType int

const (
	MailConfirmation MailType = iota + 1
	PasswordReset
	EmailSubscription
	DownloadClient
	WorkspaceInvite
)

type MailData struct {
	Username   string
	Code       string
	OrgName    string
	InviteLink string
	ZuriLogo   string
	Image2     string
}

type Mail struct {
	to      []string
	subject string
	body    string
	mtype   MailType
	data    *MailData
}

type ZcMailService struct {
	configs *utils.Configurations
}

func NewZcMailService(c *utils.Configurations) *ZcMailService {
	return &ZcMailService{configs: c}
}

// Gmail smtp setup
// To use this, Gmail need to set allowed unsafe app
func (ms *ZcMailService) LoadTemplate(mailReq *Mail) (string, error) {
	
	// include your email template here
	m := map[MailType]string{
		MailConfirmation: ms.configs.ConfirmEmailTemplate,
		PasswordReset: ms.configs.PasswordResetTemplate,
		EmailSubscription: ms.configs.EmailSubscriptionTemplate,
		DownloadClient: ms.configs.DownloadClientTemplate,
		WorkspaceInvite: ms.configs.WorkspaceInviteTemplate,
	}
	
	templateFileName, ok := m[mailReq.mtype]
	if !ok { return "", errors.New("Invalid email type or email template does not exists") }

	t, err := template.ParseFiles(templateFileName)
	if err != nil {
		return "", err
	}

	buf := new(bytes.Buffer)
	if err = t.Execute(buf, mailReq.data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (ms *ZcMailService) SendMail(mailReq *Mail) error {
	// if ms.configs.ESPType == "sendgrid"
	switch esp := strings.ToLower(ms.configs.ESPType); esp {

	case "sendgrid":

		body, err := ms.LoadTemplate(mailReq)
		if err != nil { return err }

		request := sendgrid.GetRequest(
			ms.configs.SendGridApiKey,
			"/v3/mail/send",
			"https://api.sendgrid.com",
		)

		request.Method = "POST"

		x, a := mailReq.to[0], mailReq.to[1:]
		reziever := strings.Split(x, "@")

		from := mail.NewEmail("Zuri Chat", ms.configs.SendgridEmail)
		to := mail.NewEmail(reziever[0], x)
	
		content := mail.NewContent("text/html", body)
	
		m := mail.NewV3MailInit(from, mailReq.subject, to, content)
		if len(a) > 0 {
	
			tos := make([]*mail.Email, 0)
			for _, to := range mailReq.to {
				user := strings.Split(to, "@")
				tos = append(tos, mail.NewEmail(user[0], to))
			}
	
			m.Personalizations[0].AddTos(tos...)
		}

		request.Body = mail.GetRequestBody(m)

		response, err := sendgrid.API(request)
		if err != nil {
			return err
		}

		fmt.Printf("mail sent successfully, with status code %d", response.StatusCode)
		return nil

	case "smtp":

		return nil

	case "mailgun":
		// switch to mailgun temp
		body, err := ms.LoadTemplate(mailReq)
		if err != nil { return err }

		mg := mailgun.NewMailgun(ms.configs.MailGunDomain, ms.configs.MailGunKey)
		message := mg.NewMessage(ms.configs.MailGunSenderEmail, mailReq.subject, "", mailReq.to...)
		message.SetHtml(body)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()

		if _, _, err := mg.Send(ctx, message); err != nil {
			return err
		}
		return nil

	default:
		msg := fmt.Sprintf("%s is not included in the list of email service providers", esp)
		return errors.New(msg)
	}

}

func (ms *ZcMailService) NewMail(to []string, subject string, mailType MailType, data *MailData) *Mail {
	return &Mail{
		to:      to,
		subject: subject,
		mtype:   mailType,
		data:    data,
	}
}
