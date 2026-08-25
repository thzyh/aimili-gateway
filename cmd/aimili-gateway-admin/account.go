package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thzyh/aimili-gateway/internal/auth"
	"github.com/thzyh/aimili-gateway/internal/store"
)

const (
	randomPasswordBytes = 24
	totpSecretBytes     = 20
)

type commandDependencies struct {
	Now    func() time.Time
	Random io.Reader
}

func runAccountMenu(ctx context.Context, database *store.Store, masterKeyPath string, prompts *promptReader, out io.Writer, dependencies commandDependencies) error {
	for {
		_, _ = fmt.Fprintln(out, "\nAimili Gateway 账户管理")
		_, _ = fmt.Fprintln(out, "1. 查看账户状态")
		_, _ = fmt.Fprintln(out, "2. 修改用户名")
		_, _ = fmt.Fprintln(out, "3. 生成随机新密码")
		_, _ = fmt.Fprintln(out, "4. 设置自定义新密码")
		_, _ = fmt.Fprintln(out, "5. 启用或重新登记 TOTP")
		_, _ = fmt.Fprintln(out, "6. 关闭 TOTP")
		_, _ = fmt.Fprintln(out, "7. 撤销全部登录会话")
		_, _ = fmt.Fprintln(out, "0. 退出")
		choice, err := prompts.prompt(out, "请选择：", false)
		if err != nil {
			return errors.New("read menu choice")
		}

		var operationErr error
		switch strings.TrimSpace(choice) {
		case "0":
			return nil
		case "1":
			operationErr = showAccountStatus(ctx, database, out)
		case "2":
			operationErr = changeUsername(ctx, database, prompts, out, dependencies.Now)
		case "3":
			operationErr = resetRandomPassword(ctx, database, out, dependencies)
		case "4":
			operationErr = resetCustomPassword(ctx, database, prompts, out, dependencies.Now)
		case "5":
			operationErr = enableOrResetTOTP(ctx, database, masterKeyPath, prompts, out, dependencies)
		case "6":
			operationErr = disableTOTP(ctx, database, prompts, out, dependencies.Now)
		case "7":
			operationErr = database.RevokeAllSessions(ctx)
			if operationErr == nil {
				_, _ = fmt.Fprintln(out, "全部 Gateway 登录会话已撤销。")
			}
		default:
			_, _ = fmt.Fprintln(out, "无效选择，请重新输入。")
			continue
		}
		if operationErr != nil {
			_, _ = fmt.Fprintf(out, "操作失败：%v\n", operationErr)
		}
	}
}

func showAccountStatus(ctx context.Context, database *store.Store, out io.Writer) error {
	admin, err := database.GetAdmin(ctx)
	if err != nil {
		return err
	}
	totpStatus := "已关闭"
	if admin.TOTPEnabled {
		totpStatus = "已启用"
	}
	_, _ = fmt.Fprintf(out, "当前用户名：%s\n", admin.Username)
	_, _ = fmt.Fprintf(out, "TOTP：%s\n", totpStatus)
	_, _ = fmt.Fprintf(out, "账户创建时间：%s\n", admin.CreatedAt.Local().Format(time.RFC3339))
	_, _ = fmt.Fprintf(out, "安全信息更新时间：%s\n", admin.SecurityUpdatedAt.Local().Format(time.RFC3339))
	return nil
}

func changeUsername(ctx context.Context, database *store.Store, prompts *promptReader, out io.Writer, now func() time.Time) error {
	admin, err := database.GetAdmin(ctx)
	if err != nil {
		return err
	}
	username, err := prompts.prompt(out, "新用户名：", false)
	if err != nil {
		return errors.New("read username")
	}
	username = strings.TrimSpace(username)
	if username == "" || utf8.RuneCountInString(username) > 128 || len([]byte(username)) > 256 {
		return errors.New("用户名必须为 1 至 128 个字符且不超过 256 字节")
	}
	admin.Username = username
	if err := applyAdminUpdate(ctx, database, admin, "account.username_updated", now()); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "用户名已更新，全部旧会话已撤销。")
	return nil
}

func resetRandomPassword(ctx context.Context, database *store.Store, out io.Writer, dependencies commandDependencies) error {
	password, err := generateRandomPassword(dependencies.Random)
	if err != nil {
		return err
	}
	defer clear(password)
	admin, err := database.GetAdmin(ctx)
	if err != nil {
		return err
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	admin.PasswordHash = []byte(passwordHash)
	if err := applyAdminUpdate(ctx, database, admin, "account.password_updated", dependencies.Now()); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "新随机密码（仅显示一次）：%s\n", password)
	return nil
}

func generateRandomPassword(random io.Reader) ([]byte, error) {
	raw := make([]byte, randomPasswordBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		clear(raw)
		return nil, errors.New("generate random password")
	}
	defer clear(raw)
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(encoded, raw)
	return encoded, nil
}

func resetCustomPassword(ctx context.Context, database *store.Store, prompts *promptReader, out io.Writer, now func() time.Time) error {
	passwordText, err := prompts.prompt(out, "新密码：", true)
	if err != nil {
		return errors.New("read password")
	}
	confirmationText, err := prompts.prompt(out, "再次输入新密码：", true)
	if err != nil {
		return errors.New("read password confirmation")
	}
	password := []byte(passwordText)
	confirmation := []byte(confirmationText)
	defer clear(password)
	defer clear(confirmation)
	if err := validatePassword(passwordText, password, confirmation); err != nil {
		return err
	}
	admin, err := database.GetAdmin(ctx)
	if err != nil {
		return err
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	admin.PasswordHash = []byte(passwordHash)
	if err := applyAdminUpdate(ctx, database, admin, "account.password_updated", now()); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "自定义密码已更新，全部旧会话已撤销。")
	return nil
}

func validatePassword(passwordText string, password, confirmation []byte) error {
	if utf8.RuneCount(password) < 12 || len(password) > 256 {
		return errors.New("密码必须至少包含 12 个字符且不超过 256 字节")
	}
	if strings.TrimSpace(passwordText) != passwordText {
		return errors.New("密码首尾不能包含空白")
	}
	if !equalBytes(password, confirmation) {
		return errors.New("两次输入的密码不一致")
	}
	return nil
}

func enableOrResetTOTP(ctx context.Context, database *store.Store, masterKeyPath string, prompts *promptReader, out io.Writer, dependencies commandDependencies) error {
	admin, err := database.GetAdmin(ctx)
	if err != nil {
		return err
	}
	secret := make([]byte, totpSecretBytes)
	if _, err := io.ReadFull(dependencies.Random, secret); err != nil {
		clear(secret)
		return errors.New("generate TOTP secret")
	}
	defer clear(secret)
	_, _ = fmt.Fprintln(out, "请登记以下 TOTP 地址；仅在当前终端显示：")
	_, _ = fmt.Fprintln(out, buildEnrollmentURI(admin.Username, secret))
	code, err := prompts.prompt(out, "输入新验证器生成的 6 位验证码：", true)
	if err != nil {
		return errors.New("read TOTP confirmation")
	}
	if !auth.ValidateTOTP(secret, strings.TrimSpace(code), dependencies.Now().UTC()) {
		return errors.New("新 TOTP 验证码无效，原设置未改变")
	}
	masterKey, err := loadMasterKey(masterKeyPath)
	if err != nil {
		return err
	}
	defer clear(masterKey)
	encrypted, err := auth.Seal(masterKey, secret)
	if err != nil {
		return err
	}
	admin.TOTPEnabled = true
	admin.TOTPSecretCiphertext = encrypted
	if err := applyAdminUpdate(ctx, database, admin, "account.totp_enabled", dependencies.Now()); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "TOTP 已启用或重新登记，全部旧会话已撤销。")
	return nil
}

func disableTOTP(ctx context.Context, database *store.Store, prompts *promptReader, out io.Writer, now func() time.Time) error {
	admin, err := database.GetAdmin(ctx)
	if err != nil {
		return err
	}
	if !admin.TOTPEnabled {
		_, _ = fmt.Fprintln(out, "TOTP 已处于关闭状态。")
		return nil
	}
	confirmation, err := prompts.prompt(out, "确认仅使用密码登录，请输入“关闭 TOTP”：", false)
	if err != nil {
		return errors.New("read TOTP disable confirmation")
	}
	if strings.TrimSpace(confirmation) != "关闭 TOTP" {
		return errors.New("确认词不匹配，TOTP 未关闭")
	}
	admin.TOTPEnabled = false
	admin.TOTPSecretCiphertext = nil
	if err := applyAdminUpdate(ctx, database, admin, "account.totp_disabled", now()); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "TOTP 已关闭，全部旧会话已撤销。")
	return nil
}

func applyAdminUpdate(ctx context.Context, database *store.Store, admin store.Admin, action string, timestamp time.Time) error {
	updatedAt := timestamp.UTC()
	if !updatedAt.After(admin.SecurityUpdatedAt) {
		updatedAt = admin.SecurityUpdatedAt.Add(time.Millisecond)
	}
	return database.UpdateAdminSecurityAndRevokeSessions(ctx, store.AdminSecurityUpdate{
		ExpectedSecurityUpdatedAt: admin.SecurityUpdatedAt,
		Username:                  admin.Username,
		PasswordHash:              admin.PasswordHash,
		TOTPEnabled:               admin.TOTPEnabled,
		TOTPSecretCiphertext:      admin.TOTPSecretCiphertext,
		Action:                    action,
		UpdatedAt:                 updatedAt,
	})
}
