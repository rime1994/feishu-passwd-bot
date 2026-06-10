package ldap

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf16"

	"feishu-passwd-bot/config"

	goldap "github.com/go-ldap/ldap/v3"
	"golang.org/x/crypto/md4"
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

// ChangePassword 通过 LDAP admin 权限修改指定 DN 的密码，并同步更新 sambaNTPassword。
// 两步操作强一致：任一失败均返回错误。
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

	// 同步写入 sambaNTPassword，供 FreeRADIUS PEAP/MSCHAPv2 认证使用
	ntHashHex := ntHash(newPassword)
	modReq := goldap.NewModifyRequest(userDN, nil)
	modReq.Replace("sambaNTPassword", []string{ntHashHex})
	if err := conn.Modify(modReq); err != nil {
		return fmt.Errorf("sambaNTPassword 写入失败: %w", err)
	}

	return nil
}

// ntHash 计算 Windows NT Hash：MD4(UTF-16LE(password))，返回大写 hex 字符串。
func ntHash(password string) string {
	utf16le := utf16.Encode([]rune(password))
	buf := make([]byte, len(utf16le)*2)
	for i, r := range utf16le {
		binary.LittleEndian.PutUint16(buf[i*2:], r)
	}
	h := md4.New()
	h.Write(buf)
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}
