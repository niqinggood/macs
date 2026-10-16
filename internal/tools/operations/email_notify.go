package operations

import (
	"fmt"
	"net/smtp"
	"time"
)

// SMTPNotificationConfig is the minimal configuration required by operational
// email notifications. Receiving-mail and UI settings stay outside this layer.
type SMTPNotificationConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	FromAddress string
}

func SendNotificationEmail(config SMTPNotificationConfig, recipient, subject, body string) error {
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		config.FromAddress,
		recipient,
		subject,
		body,
	)
	auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	return smtp.SendMail(addr, auth, config.FromAddress, []string{recipient}, []byte(msg))
}

func WeeklyHealthReportBody(stats string, generated time.Time) string {
	return fmt.Sprintf(
		"OpenKitty Weekly Health Report\n\n%s\nGenerated: %s",
		stats,
		generated.Format("2006-01-02 15:04"),
	)
}

func TaskFailureNotification(deName, task, errorMessage string, occurred time.Time) (subject, body string) {
	subject = fmt.Sprintf("[OpenKitty] %s task failed", deName)
	body = fmt.Sprintf(
		"DE %s task failed.\n\nTask: %s\nError: %s\nTime: %s",
		deName,
		task,
		errorMessage,
		occurred.Format("2006-01-02 15:04:05"),
	)
	return subject, body
}
