package config

import (
	"fmt"
	"net/url"
	"strings"
)

func (c *Config) validateJobMaster() error {
	masterURL := strings.TrimSpace(c.JobMaster.URL)
	secret := strings.TrimSpace(c.JobMaster.M2MSecret)
	if masterURL == "" && secret == "" {
		return nil
	}
	if masterURL == "" {
		return fmt.Errorf("JOB_MASTER_URL is required when JOB_MASTER_M2M_SECRET is set")
	}
	if secret == "" {
		return fmt.Errorf("JOB_MASTER_M2M_SECRET is required when JOB_MASTER_URL is set")
	}
	u, err := url.Parse(strings.TrimRight(masterURL, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("JOB_MASTER_URL must be an absolute HTTP or HTTPS URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("JOB_MASTER_URL must not include credentials, query parameters, or fragments")
	}
	if c.JobMaster.TimeoutSeconds == 0 {
		c.JobMaster.TimeoutSeconds = 30
	}
	if c.JobMaster.PollIntervalSeconds == 0 {
		c.JobMaster.PollIntervalSeconds = 3
	}
	if c.JobMaster.PollTimeoutSeconds == 0 {
		c.JobMaster.PollTimeoutSeconds = 1800
	}
	if c.JobMaster.TimeoutSeconds <= 0 {
		return fmt.Errorf("JOB_MASTER_HTTP_TIMEOUT_SECONDS must be positive")
	}
	if c.JobMaster.PollIntervalSeconds <= 0 {
		return fmt.Errorf("JOB_MASTER_POLL_INTERVAL_SECONDS must be positive")
	}
	if c.JobMaster.PollTimeoutSeconds <= 0 {
		return fmt.Errorf("JOB_MASTER_POLL_TIMEOUT_SECONDS must be positive")
	}
	if c.HTTP.AppEnv == "production" && len(secret) < 32 {
		return fmt.Errorf("JOB_MASTER_M2M_SECRET must be at least 32 characters in production")
	}
	return nil
}
