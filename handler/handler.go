package handler

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
	"unicode"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcard "github.com/larksuite/oapi-sdk-go/v3/card"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"feishu-passwd-bot/card"
	"feishu-passwd-bot/config"
	botldap "feishu-passwd-bot/ldap"
)

// 触发关键词
var triggerKeywords = []string{"修改密码", "改密码", "重置密码", "changepassword", "change password"}

type Handler struct {
	client     *lark.Client
	ldapClient *botldap.Client
	cfg        *config.Config
}

func New(cfg *config.Config, ldapClient *botldap.Client) *Handler {
	return &Handler{
		client:     lark.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret),
		ldapClient: ldapClient,
		cfg:        cfg,
	}
}

// OnMessage 处理用户发给 Bot 的消息
func (h *Handler) OnMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if event.Event == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil {
		return nil
	}
	openID := strVal(event.Event.Sender.SenderId.OpenId)
	if openID == "" {
		return nil
	}

	// 提取消息文本
	text := extractMsgText(event.Event.Message)

	// 检查是否触发关键词
	if !isTriggered(text) {
		return h.sendText(ctx, openID, "发送「修改密码」开始自助修改 LDAP 密码 🔐")
	}

	feishuUserID, err := h.resolveUserID(ctx, openID)
	if err != nil {
		log.Printf("[handler] resolveUserID failed open_id=%s err=%v", openID, err)
	}
	ldapUID := ""
	if feishuUserID != "" {
		log.Printf("[handler] resolved user_id=%s for open_id=%s", feishuUserID, openID)
		u, err := h.ldapClient.FindUser(feishuUserID)
		if err != nil {
			log.Printf("[handler] FindUser failed user_id=%s err=%v", feishuUserID, err)
			return h.sendText(ctx, openID, "查询 LDAP 账号失败，请稍后再试或联系管理员。")
		}
		if u == nil {
			log.Printf("[handler] no LDAP user found for user_id=%s", feishuUserID)
			return h.sendText(ctx, openID, "未找到您的 LDAP 账号，可能飞书同步尚未完成，请稍等几分钟后再试。如长时间未解决请联系管理员。")
		}
		ldapUID = u.UID
		log.Printf("[handler] LDAP uid=%s for user_id=%s", ldapUID, feishuUserID)
	} else {
		log.Printf("[handler] feishuUserID empty for open_id=%s", openID)
	}
	return h.sendCard(ctx, openID, card.PasswordFormCard(ldapUID))
}

// OnCardAction 处理交互卡片回调（用户提交表单）
func (h *Handler) OnCardAction(ctx context.Context, action *larkcard.CardAction) (any, error) {
	actionVal, _ := action.Action.Value["action"].(string)
	openID := action.OpenID
	// user_id is sent directly in the card callback; avoids an extra Contact API round-trip.
	userID := action.UserID
	msgID := action.OpenMessageID

	switch actionVal {
	case "cancel":
		go h.updateCard(msgID, "cancelled", card.CancelCard())
		return h.ack(), nil

	case "retry":
		go h.updateCard(msgID, "form", card.PasswordFormCard(""))
		return h.ack(), nil

	case "submit_password":
		return h.handleSubmit(ctx, openID, userID, msgID, action.Action.FormValue)

	default:
		return nil, nil
	}
}

// updateCard 通过 Im.Message.Patch 替换卡片内容。
// Schema 2.0 不支持从 callback response 直接更新卡片，必须走 Patch API。
func (h *Handler) updateCard(msgID, state, cardJSON string) {
	if msgID == "" {
		log.Printf("[card] patch skipped state=%s reason=missing_message_id", state)
		return
	}
	started := time.Now()
	log.Printf("[card] patch started state=%s message_id=%s", state, msgID)
	ctx := context.Background()
	_, err := h.client.Im.Message.Patch(ctx,
		larkim.NewPatchMessageReqBuilder().
			MessageId(msgID).
			Body(larkim.NewPatchMessageReqBodyBuilder().
				Content(cardJSON).
				Build()).
			Build(),
	)
	if err != nil {
		log.Printf("[card] patch failed state=%s message_id=%s duration=%s err=%v", state, msgID, time.Since(started), err)
		return
	}
	log.Printf("[card] patch complete state=%s message_id=%s duration=%s", state, msgID, time.Since(started))
}

// ack 返回飞书 schema 2.0 回调的空确认响应，防止触发重试和表单重置。
func (h *Handler) ack() *larkcard.CustomResp {
	return &larkcard.CustomResp{StatusCode: 200, Body: map[string]any{}}
}

// handleSubmit 处理密码提交
func (h *Handler) handleSubmit(ctx context.Context, openID, userID, msgID string, formValue map[string]any) (any, error) {
	newPass, _ := formValue["new_password"].(string)
	confirmPass, _ := formValue["confirm_password"].(string)

	// 密码校验
	if err := validatePassword(newPass, confirmPass); err != nil {
		go h.updateCard(msgID, "error", card.ErrorCard(err.Error()))
		return h.ack(), nil
	}

	// 优先使用卡片回调中的 user_id，否则调 Contact API 解析
	if userID == "" {
		var err error
		userID, err = h.resolveUserID(ctx, openID)
		if err != nil || userID == "" {
			log.Printf("[handler] 获取 user_id 失败 open_id=%s err=%v", openID, err)
			go h.updateCard(msgID, "error", card.ErrorCard("无法获取您的账号信息，请联系管理员"))
			return h.ack(), nil
		}
	}

	userDN, err := h.ldapClient.FindUserDN(userID)
	if err != nil {
		log.Printf("[handler] LDAP 查询失败 user_id=%s err=%v", userID, err)
		go h.updateCard(msgID, "error", card.ErrorCard("查询 LDAP 账号失败，请稍后重试"))
		return h.ack(), nil
	}
	if userDN == "" {
		go h.updateCard(msgID, "error", card.ErrorCard("未找到您的 LDAP 账号，请确认已完成飞书同步"))
		return h.ack(), nil
	}

	if err := h.ldapClient.ChangePassword(userDN, newPass); err != nil {
		log.Printf("[handler] 改密失败 dn=%s err=%v", userDN, err)
		go h.updateCard(msgID, "error", card.ErrorCard("密码修改失败，请稍后重试"))
		return h.ack(), nil
	}

	log.Printf("[handler] 密码修改成功 dn=%s", userDN)
	go h.updateCard(msgID, "success", card.SuccessCard())
	return h.ack(), nil
}

// resolveUserID 通过 open_id 获取飞书 user_id
// 需要应用有 contact:user.id:readonly 权限
func (h *Handler) resolveUserID(ctx context.Context, openID string) (string, error) {
	resp, err := h.client.Contact.User.Get(ctx,
		larkcontact.NewGetUserReqBuilder().
			UserId(openID).
			UserIdType("open_id").
			Build(),
	)
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", nil
	}
	if resp.Data == nil || resp.Data.User == nil || resp.Data.User.UserId == nil {
		return "", nil
	}
	return *resp.Data.User.UserId, nil
}

// sendCard 发送交互卡片给用户
func (h *Handler) sendCard(ctx context.Context, openID, cardJSON string) error {
	_, err := h.client.Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("open_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(openID).
				MsgType("interactive").
				Content(cardJSON).
				Build()).
			Build(),
	)
	return err
}

// sendText 发送文本消息给用户
func (h *Handler) sendText(ctx context.Context, openID, text string) error {
	content, _ := json.Marshal(map[string]string{"text": text})
	_, err := h.client.Im.Message.Create(ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("open_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(openID).
				MsgType("text").
				Content(string(content)).
				Build()).
			Build(),
	)
	return err
}

// validatePassword 校验密码强度
func validatePassword(newPass, confirmPass string) error {
	if newPass == "" || confirmPass == "" {
		return errorf("密码不能为空")
	}
	if newPass != confirmPass {
		return errorf("两次输入的密码不一致")
	}
	if len([]rune(newPass)) < 8 {
		return errorf("密码至少需要 8 位")
	}
	hasLetter, hasDigit := false, false
	for _, r := range newPass {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return errorf("密码必须同时包含字母和数字")
	}
	return nil
}

// extractMsgText 从飞书消息 content JSON 中提取纯文本
func extractMsgText(msg *larkim.EventMessage) string {
	if msg == nil || msg.Content == nil {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*msg.Content), &m); err != nil {
		return ""
	}
	return m["text"]
}

func isTriggered(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, kw := range triggerKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type simpleError string

func (e simpleError) Error() string { return string(e) }
func errorf(msg string) error       { return simpleError(msg) }
