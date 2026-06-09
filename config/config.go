package config

import (
	"log"
	"os"
)

type Config struct {
	Feishu FeishuConfig
	LDAP   LDAPConfig
	Port   string
}

type FeishuConfig struct {
	AppID             string
	AppSecret         string
	VerificationToken string
	EncryptKey        string
}

type LDAPConfig struct {
	Addr      string
	AdminDN   string
	AdminPass string
	BaseDN    string
}

func Load() *Config {
	return &Config{
		Feishu: FeishuConfig{
			AppID:             mustEnv("FEISHU_APP_ID"),
			AppSecret:         mustEnv("FEISHU_APP_SECRET"),
			VerificationToken: mustEnv("FEISHU_VERIFICATION_TOKEN"),
			EncryptKey:        getEnv("FEISHU_ENCRYPT_KEY", ""),
		},
		LDAP: LDAPConfig{
			Addr:      getEnv("LDAP_ADDR", "ldap://go-ldap-admin-openldap:389"),
			AdminDN:   mustEnv("LDAP_ADMIN_DN"),
			AdminPass: mustEnv("LDAP_ADMIN_PASS"),
			BaseDN:    mustEnv("LDAP_BASE_DN"),
		},
		Port: getEnv("PORT", "8080"),
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[config] 必填环境变量未设置: %s", key)
	}
	return v
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
