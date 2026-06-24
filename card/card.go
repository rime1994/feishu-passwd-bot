package card

import "encoding/json"

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// PasswordFormCard 密码修改表单卡片，ldapUID 为 LDAP 登录名
func PasswordFormCard(ldapUID string) string {
	desc := "身份已通过飞书验证，请填写新密码。\n\n**密码要求：** 至少 8 位，包含字母和数字。"
	if ldapUID != "" {
		desc = "**账号：** " + ldapUID + "\n\n" + desc
	}
	return mustJSON(map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": "🔐 修改 LDAP 密码"},
			"template": "blue",
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": desc,
				},
				map[string]any{
					"tag":  "form",
					"name": "password_form",
					"elements": []any{
						map[string]any{
							"tag":         "input",
							"name":        "new_password",
							"label":       map[string]any{"tag": "plain_text", "content": "新密码"},
							"placeholder": map[string]any{"tag": "plain_text", "content": "请输入新密码（至少8位，字母+数字）"},
						},
						map[string]any{
							"tag":         "input",
							"name":        "confirm_password",
							"label":       map[string]any{"tag": "plain_text", "content": "确认密码"},
							"placeholder": map[string]any{"tag": "plain_text", "content": "请再次输入新密码"},
						},
						map[string]any{
							"tag":              "button",
							"name":             "Button_submit_password",
							"form_action_type": "submit",
							"text":             map[string]any{"tag": "plain_text", "content": "✅ 确认修改"},
							"type":             "primary",
							"behaviors": []any{
								map[string]any{
									"type":  "callback",
									"value": map[string]any{"action": "submit_password"},
								},
							},
							"confirm": map[string]any{
								"title": map[string]any{"tag": "plain_text", "content": "确认修改密码？"},
								"text":  map[string]any{"tag": "plain_text", "content": "新密码将立即生效"},
							},
						},
						map[string]any{
							"tag":  "button",
							"name": "Button_cancel",
							"text": map[string]any{"tag": "plain_text", "content": "取消"},
							"type": "default",
							"behaviors": []any{
								map[string]any{
									"type":  "callback",
									"value": map[string]any{"action": "cancel"},
								},
							},
						},
					},
				},
			},
		},
	})
}

// SuccessCard 成功卡片
func SuccessCard() string {
	return mustJSON(map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": "✅ 密码修改成功"},
			"template": "green",
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": "您的 LDAP 密码已成功修改，新密码即刻生效。",
				},
			},
		},
	})
}

// ErrorCard 错误卡片
func ErrorCard(msg string) string {
	return mustJSON(map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": "❌ 操作失败"},
			"template": "red",
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": msg + "\n\n如需帮助请联系管理员。",
				},
				map[string]any{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": "🔄 重试"},
					"type": "primary",
					"behaviors": []any{
						map[string]any{
							"type":  "callback",
							"value": map[string]any{"action": "retry"},
						},
					},
				},
			},
		},
	})
}

// CancelCard 取消卡片
func CancelCard() string {
	return mustJSON(map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": "已取消"},
			"template": "grey",
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": "操作已取消。如需修改密码，请再次发送「修改密码」。",
				},
			},
		},
	})
}
