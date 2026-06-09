package main

import (
	"context"
	"io"
	"log"
	"net/http"

	larkcard "github.com/larksuite/oapi-sdk-go/v3/card"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"feishu-passwd-bot/config"
	"feishu-passwd-bot/handler"
	botldap "feishu-passwd-bot/ldap"
)

// sdkHandler is a common interface for both EventDispatcher and CardActionHandler
type sdkHandler interface {
	Handle(ctx context.Context, req *larkevent.EventReq) *larkevent.EventResp
}

// adaptSDK wraps an sdkHandler as a standard http.HandlerFunc
func adaptSDK(h sdkHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body error", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		req := &larkevent.EventReq{
			Header:     map[string][]string(r.Header),
			Body:       body,
			RequestURI: r.RequestURI,
		}

		resp := h.Handle(r.Context(), req)
		if resp == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Copy response headers
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}

		if resp.StatusCode != 0 {
			w.WriteHeader(resp.StatusCode)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		if len(resp.Body) > 0 {
			_, _ = w.Write(resp.Body)
		}
	}
}

func main() {
	cfg := config.Load()

	ldapClient := botldap.NewClient(&cfg.LDAP)
	h := handler.New(cfg, ldapClient)

	// 飞书事件分发器（处理消息事件）
	eventDispatcher := dispatcher.NewEventDispatcher(
		cfg.Feishu.VerificationToken,
		cfg.Feishu.EncryptKey,
	).OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		return h.OnMessage(ctx, event)
	})

	// 飞书卡片回调处理器
	cardHandler := larkcard.NewCardActionHandler(
		cfg.Feishu.VerificationToken,
		cfg.Feishu.EncryptKey,
		func(ctx context.Context, action *larkcard.CardAction) (any, error) {
			return h.OnCardAction(ctx, action)
		},
	)

	// 注册路由（使用自定义适配器桥接 SDK 与 net/http）
	http.Handle("/webhook/event", adaptSDK(eventDispatcher))
	http.Handle("/webhook/card", adaptSDK(cardHandler))
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := ":" + cfg.Port
	log.Printf("[main] feishu-passwd-bot 启动，监听 %s", addr)
	log.Printf("[main] 事件地址: http://0.0.0.0%s/webhook/event", addr)
	log.Printf("[main] 卡片地址: http://0.0.0.0%s/webhook/card", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("[main] 启动失败: %v", err)
	}
}
