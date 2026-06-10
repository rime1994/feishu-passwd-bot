package ldap

import (
	"fmt"

	"feishu-passwd-bot/config"

	goldap "github.com/go-ldap/ldap/v3"
)

type Client struct {
	cfg *config.LDAPConfig
}

func NewClient(cfg *config.LDAPConfig) *Client {
	return &Client{cfg: cfg}
}

type User struct {
	DN  string
	UID string
}

// FindUser 通过飞书 user_id 在 LDAP 中查找用户，匹配 employeeType = feishu_{userID}
func (c *Client) FindUser(feishuUserID string) (*User, error) {
	conn, err := goldap.DialURL(c.cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("LDAP 连接失败: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(c.cfg.AdminDN, c.cfg.AdminPass); err != nil {
		return nil, fmt.Errorf("LDAP 认证失败: %w", err)
	}

	filter := fmt.Sprintf(
		"(&(objectClass=inetOrgPerson)(employeeType=feishu_%s))",
		goldap.EscapeFilter(feishuUserID),
	)
	req := goldap.NewSearchRequest(
		c.cfg.BaseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		1, 0, false,
		filter,
		[]string{"dn", "uid"},
		nil,
	)

	result, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("LDAP 查询失败: %w", err)
	}
	if len(result.Entries) == 0 {
		return nil, nil // 未找到
	}
	entry := result.Entries[0]
	return &User{
		DN:  entry.DN,
		UID: entry.GetAttributeValue("uid"),
	}, nil
}

// FindUserDN 兼容旧调用，返回用户 DN
func (c *Client) FindUserDN(feishuUserID string) (string, error) {
	u, err := c.FindUser(feishuUserID)
	if err != nil || u == nil {
		return "", err
	}
	return u.DN, nil
}

// ChangePassword 通过 LDAP admin 权限修改指定 DN 的密码
func (c *Client) ChangePassword(userDN, newPassword string) error {
	conn, err := goldap.DialURL(c.cfg.Addr)
	if err != nil {
		return fmt.Errorf("LDAP 连接失败: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(c.cfg.AdminDN, c.cfg.AdminPass); err != nil {
		return fmt.Errorf("LDAP 认证失败: %w", err)
	}

	req := goldap.NewPasswordModifyRequest(userDN, "", newPassword)
	if _, err := conn.PasswordModify(req); err != nil {
		return fmt.Errorf("密码修改失败: %w", err)
	}
	return nil
}
