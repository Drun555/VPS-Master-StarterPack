package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type AddServerRequest struct {
	DisplayName  string `json:"display_name"`
	Address      string `json:"address"`
	PrivateKey   string `json:"private_key"`
	Passphrase   string `json:"passphrase"`
	PasswordAuth bool   `json:"password_auth"`
	Password     string `json:"password"`
	PublicKey    string `json:"public_key"`
	DuckDNSURL   string `json:"duckdns_url"`
	DuckDNSToken string `json:"duckdns_token"`
}

type UpdateServerRequest struct {
	DisplayName string `json:"display_name"`
}

type AddUserRequest struct {
	Email string `json:"email"`
}

func (app *App) provisionServer(ctx context.Context, request AddServerRequest, reporter *JobReporter) error {
	app.operationMu.Lock()
	defer app.operationMu.Unlock()

	host, port, normalizedAddress, err := parseSSHAddress(request.Address)
	if err != nil {
		return err
	}
	domain, err := normalizeDuckDNS(request.DuckDNSURL)
	if err != nil {
		return err
	}
	displayName, err := normalizeServerDisplayName(request.DisplayName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(request.DuckDNSToken) == "" {
		return fmt.Errorf("DuckDNS token is required")
	}
	signer, err := parsePrivateSigner(request.PrivateKey, request.Passphrase)
	if err != nil {
		return err
	}
	publicKey := authorizedPublicKey(signer)
	if request.PasswordAuth {
		if request.Password == "" {
			return fmt.Errorf("SSH password is required in password mode")
		}
		if strings.TrimSpace(request.PublicKey) == "" {
			return fmt.Errorf("public key is required in password mode")
		}
		if err := validatePublicKeyMatches(signer, request.PublicKey); err != nil {
			return err
		}
		publicKey = strings.TrimSpace(request.PublicKey)
	}

	state := app.store.Snapshot()
	for _, existing := range state.Servers {
		if strings.EqualFold(existing.Address, normalizedAddress) || strings.EqualFold(existing.DuckDNSURL, domain) {
			return fmt.Errorf("server with this address or DuckDNS domain already exists")
		}
	}

	reporter.Log("Подключение к root@%s…", normalizedAddress)
	client, fingerprint, err := dialSSH(ctx, host, port, SSHCredential{
		PrivateKey: request.PrivateKey, Passphrase: request.Passphrase,
		Password: request.Password, UsePassword: request.PasswordAuth,
	}, "")
	if err != nil {
		return err
	}
	defer client.Close()
	reporter.Log("SSH host key принят по TOFU: %s", fingerprint)

	command := "curl -fsSL " + shellQuote(app.config.SlaveSetupURL) + " | bash -s --" +
		" --setup-ssh-key " + shellQuote(publicKey) +
		" --duckdns-token " + shellQuote(request.DuckDNSToken) +
		" --duckdns-url " + shellQuote(domain) +
		" --certbot-email " + shellQuote(app.config.CertbotEmail) +
		" --hide-credentials"
	reporter.Log("Запуск Slave setup.sh…")
	if err := runSSHCommand(client, command, reporter); err != nil {
		return fmt.Errorf("Slave setup завершился с ошибкой: %w. Войдите на VPS вручную и выполните uninstall.sh", err)
	}

	credentials, err := readSSHFile(client, "/opt/vps-reality/runtime/vps-reality-credentials")
	if err != nil {
		return fmt.Errorf("setup завершился, но credentials не прочитаны: %w. Выполните uninstall.sh вручную", err)
	}
	apiUsername, apiPassword, err := parseCredentials(credentials)
	if err != nil {
		return fmt.Errorf("setup завершился, но credentials некорректны: %w. Выполните uninstall.sh вручную", err)
	}

	id, err := randomID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	server := Server{
		ID: id, DisplayName: displayName, Address: normalizedAddress, SSHHost: host, SSHPort: port,
		SSHPrivateKey: strings.TrimSpace(request.PrivateKey), SSHPassphrase: request.Passphrase,
		SSHHostFingerprint: fingerprint, PublicKey: publicKey,
		DuckDNSURL: domain, DuckDNSToken: request.DuckDNSToken,
		APIUsername: apiUsername, APIPassword: apiPassword,
		Status: "ready", CreatedAt: now, UpdatedAt: now,
	}
	if err := app.store.Update(func(state *State) error {
		for _, existing := range state.Servers {
			if strings.EqualFold(existing.Address, normalizedAddress) || strings.EqualFold(existing.DuckDNSURL, domain) {
				return fmt.Errorf("server with this address or DuckDNS domain already exists")
			}
		}
		state.Servers = append(state.Servers, server)
		return nil
	}); err != nil {
		return fmt.Errorf("setup завершился, но сервер не сохранён: %w. Выполните uninstall.sh вручную", err)
	}
	reporter.Log("Сервер сохранён. Синхронизация существующих пользователей…")

	users := app.store.Snapshot().Users
	warnings := 0
	for _, user := range users {
		if err := app.ensureUserOnServer(ctx, server, user, reporter); err != nil {
			warnings++
			reporter.Warning("%s: %v", user.Email, err)
		}
	}
	if warnings > 0 {
		_ = app.setServerStatus(server.ID, "partial", fmt.Sprintf("не синхронизировано пользователей: %d", warnings))
	} else {
		_ = app.setServerStatus(server.ID, "ready", "")
	}
	reporter.Log("Slave успешно установлен: %s", domain)
	return nil
}

func (app *App) cleanupFailedServer(ctx context.Context, request AddServerRequest, reporter *JobReporter) error {
	app.operationMu.Lock()
	defer app.operationMu.Unlock()

	host, port, normalizedAddress, err := parseSSHAddress(request.Address)
	if err != nil {
		return err
	}
	domain, err := normalizeDuckDNS(request.DuckDNSURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(app.config.SlaveUninstallURL) == "" {
		return fmt.Errorf("Slave uninstall URL is not configured")
	}

	credentials := []SSHCredential{{PrivateKey: request.PrivateKey, Passphrase: request.Passphrase}}
	if request.PasswordAuth {
		credentials = append(credentials, SSHCredential{
			PrivateKey: request.PrivateKey, Passphrase: request.Passphrase,
			Password: request.Password, UsePassword: true,
		})
	}

	var clientError error
	for index, credential := range credentials {
		mode := "SSH-ключу"
		if credential.UsePassword {
			mode = "паролю"
		}
		reporter.Log("Подключение к root@%s по %s для удаления…", normalizedAddress, mode)
		client, fingerprint, dialErr := dialSSH(ctx, host, port, credential, "")
		if dialErr != nil {
			clientError = dialErr
			if index+1 < len(credentials) {
				reporter.Warning("Подключение по ключу не удалось; пробуем исходный пароль.")
			}
			continue
		}
		reporter.Log("SSH host key принят по TOFU: %s", fingerprint)
		defer client.Close()
		command := "curl -fsSL " + shellQuote(app.config.SlaveUninstallURL) + " | bash -s --" +
			" --yes --domain " + shellQuote(domain)
		reporter.Log("Запуск Slave uninstall.sh…")
		if err := runSSHCommand(client, command, reporter); err != nil {
			return fmt.Errorf("uninstall.sh завершился с ошибкой: %w", err)
		}
		reporter.Log("Следы неудачной установки удалены.")
		return nil
	}
	return fmt.Errorf("SSH-подключение для удаления не выполнено: %w", clientError)
}

func (app *App) createUser(ctx context.Context, email string, reporter *JobReporter) error {
	app.operationMu.Lock()
	defer app.operationMu.Unlock()
	email = strings.TrimSpace(email)
	if !validProfileName(email) {
		return fmt.Errorf("profile name must contain between 1 and 254 bytes")
	}
	normalized := normalizeEmail(email)
	state := app.store.Snapshot()
	for _, existing := range state.Users {
		if normalizeEmail(existing.Email) == normalized {
			return fmt.Errorf("user with this email already exists")
		}
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	user := User{ID: id, Email: email, SubscriptionToken: token, Links: make(map[string]UserLink), CreatedAt: now, UpdatedAt: now}
	if err := app.store.Update(func(state *State) error {
		for _, existing := range state.Users {
			if normalizeEmail(existing.Email) == normalized {
				return fmt.Errorf("user with this email already exists")
			}
		}
		state.Users = append(state.Users, user)
		return nil
	}); err != nil {
		return err
	}
	reporter.Log("Пользователь %s сохранён в Master.", email)

	servers := app.store.Snapshot().Servers
	for _, server := range servers {
		if err := app.ensureUserOnServer(ctx, server, user, reporter); err != nil {
			reporter.Warning("%s: %v", server.DuckDNSURL, err)
		}
	}
	return nil
}

func (app *App) retryUser(ctx context.Context, userID string, reporter *JobReporter) error {
	app.operationMu.Lock()
	defer app.operationMu.Unlock()
	state := app.store.Snapshot()
	user, _ := findUser(&state, userID)
	if user == nil {
		return fmt.Errorf("user not found")
	}
	for _, server := range state.Servers {
		if err := app.ensureUserOnServer(ctx, server, *user, reporter); err != nil {
			reporter.Warning("%s: %v", server.DuckDNSURL, err)
		}
	}
	return nil
}

func (app *App) ensureUserOnServer(ctx context.Context, server Server, user User, reporter *JobReporter) error {
	reporter.Log("%s → %s: регистрация…", user.Email, server.DuckDNSURL)
	record, err := app.slaves.Register(ctx, server, user.Email)
	if err != nil {
		var apiError *SlaveAPIError
		if errors.As(err, &apiError) && apiError.Status == 409 {
			records, listErr := app.slaves.List(ctx, server)
			if listErr == nil {
				for _, existing := range records {
					if normalizeEmail(existing.Email) == normalizeEmail(user.Email) {
						record = existing
						err = nil
						break
					}
				}
			}
		}
	}
	if err != nil {
		_ = app.setUserLink(user.ID, server.ID, UserLink{ServerID: server.ID, Status: "error", LastError: err.Error(), UpdatedAt: time.Now().UTC()})
		return err
	}
	link := UserLink{ServerID: server.ID, ClientID: record.ID, URI: record.URI, Status: "ready", UpdatedAt: time.Now().UTC()}
	if err := app.setUserLink(user.ID, server.ID, link); err != nil {
		return err
	}
	reporter.Log("%s → %s: успешно.", user.Email, server.DuckDNSURL)
	return nil
}

func (app *App) deleteUser(ctx context.Context, userID string, reporter *JobReporter) error {
	app.operationMu.Lock()
	defer app.operationMu.Unlock()
	state := app.store.Snapshot()
	user, _ := findUser(&state, userID)
	if user == nil {
		return fmt.Errorf("user not found")
	}
	deleted := *user
	if err := app.store.Update(func(state *State) error {
		_, index := findUser(state, userID)
		if index < 0 {
			return fmt.Errorf("user not found")
		}
		state.Users = append(state.Users[:index], state.Users[index+1:]...)
		return nil
	}); err != nil {
		return err
	}
	reporter.Log("Пользователь удалён из Master; subscription URL уже недействителен.")
	for _, server := range state.Servers {
		records, err := app.slaves.List(ctx, server)
		if err != nil {
			reporter.Warning("%s: /list недоступен: %v", server.DuckDNSURL, err)
			continue
		}
		for _, record := range records {
			if normalizeEmail(record.Email) != normalizeEmail(deleted.Email) {
				continue
			}
			removed, removeErr := app.slaves.Remove(ctx, server, record.ID)
			if removeErr != nil || !removed {
				reporter.Warning("%s: клиент %s не удалён: %v", server.DuckDNSURL, record.ID, removeErr)
			} else {
				reporter.Log("%s: клиент удалён.", server.DuckDNSURL)
			}
		}
	}
	return nil
}

func (app *App) deleteServer(ctx context.Context, serverID, mode string, reporter *JobReporter) error {
	app.operationMu.Lock()
	defer app.operationMu.Unlock()
	state := app.store.Snapshot()
	server, _ := findServer(&state, serverID)
	if server == nil {
		return fmt.Errorf("server not found")
	}
	if mode == "forget" {
		reporter.Log("Сервер удаляется только из Master.")
		return app.forgetServer(serverID)
	}
	if mode != "uninstall" {
		return fmt.Errorf("delete mode must be uninstall or forget")
	}
	reporter.Log("Подключение к %s для uninstall…", server.Address)
	client, _, err := dialSSH(ctx, server.SSHHost, server.SSHPort, SSHCredential{
		PrivateKey: server.SSHPrivateKey, Passphrase: server.SSHPassphrase,
	}, server.SSHHostFingerprint)
	if err != nil {
		_ = app.setServerStatus(server.ID, "error", err.Error())
		return fmt.Errorf("SSH uninstall не выполнен: %w. Выполните uninstall.sh вручную", err)
	}
	defer client.Close()
	command := "/opt/vps-reality/uninstall.sh --yes --domain " + shellQuote(server.DuckDNSURL)
	if err := runSSHCommand(client, command, reporter); err != nil {
		_ = app.setServerStatus(server.ID, "error", err.Error())
		return fmt.Errorf("uninstall.sh завершился с ошибкой: %w. Выполните его вручную", err)
	}
	return app.forgetServer(serverID)
}

func (app *App) fullSync(ctx context.Context, reporter *JobReporter) error {
	app.operationMu.Lock()
	defer app.operationMu.Unlock()
	state := app.store.Snapshot()
	masterUsers := make(map[string]User, len(state.Users))
	for _, user := range state.Users {
		masterUsers[normalizeEmail(user.Email)] = user
	}

	for _, server := range state.Servers {
		serverWarnings := 0
		reporter.Log("%s: чтение списка клиентов…", server.DuckDNSURL)
		records, err := app.slaves.List(ctx, server)
		if err != nil {
			reporter.Warning("%s: %v", server.DuckDNSURL, err)
			_ = app.setServerStatus(server.ID, "error", err.Error())
			continue
		}
		groups := make(map[string][]SlaveRecord)
		for _, record := range records {
			groups[normalizeEmail(record.Email)] = append(groups[normalizeEmail(record.Email)], record)
		}

		for normalized, user := range masterUsers {
			matches := groups[normalized]
			if len(matches) == 0 {
				if err := app.ensureUserOnServer(ctx, server, user, reporter); err != nil {
					serverWarnings++
					reporter.Warning("%s: %s не добавлен: %v", server.DuckDNSURL, user.Email, err)
				}
				continue
			}
			sort.SliceStable(matches, func(i, j int) bool { return matches[i].CreatedAt.Before(matches[j].CreatedAt) })
			chosen := matches[0]
			if stored, exists := user.Links[server.ID]; exists {
				for _, candidate := range matches {
					if candidate.ID == stored.ClientID {
						chosen = candidate
						break
					}
				}
			}
			_ = app.setUserLink(user.ID, server.ID, UserLink{ServerID: server.ID, ClientID: chosen.ID, URI: chosen.URI, Status: "ready", UpdatedAt: time.Now().UTC()})
			for _, duplicate := range matches {
				if duplicate.ID == chosen.ID {
					continue
				}
				if removed, removeErr := app.slaves.Remove(ctx, server, duplicate.ID); removeErr != nil || !removed {
					serverWarnings++
					reporter.Warning("%s: дубликат %s не удалён: %v", server.DuckDNSURL, duplicate.ID, removeErr)
				} else {
					reporter.Log("%s: удалён дубликат %s.", server.DuckDNSURL, duplicate.ID)
				}
			}
		}

		for normalized, extras := range groups {
			if _, exists := masterUsers[normalized]; exists {
				continue
			}
			for _, extra := range extras {
				if removed, removeErr := app.slaves.Remove(ctx, server, extra.ID); removeErr != nil || !removed {
					serverWarnings++
					reporter.Warning("%s: лишний клиент %s не удалён: %v", server.DuckDNSURL, extra.ID, removeErr)
				} else {
					reporter.Log("%s: удалён лишний клиент %s.", server.DuckDNSURL, extra.ID)
				}
			}
		}
		if serverWarnings > 0 {
			_ = app.setServerStatus(server.ID, "partial", fmt.Sprintf("ошибок синхронизации: %d", serverWarnings))
		} else {
			_ = app.setServerStatus(server.ID, "ready", "")
		}
	}
	return nil
}

func (app *App) setUserLink(userID, serverID string, link UserLink) error {
	return app.store.Update(func(state *State) error {
		user, _ := findUser(state, userID)
		if user == nil {
			return fmt.Errorf("user not found")
		}
		if user.Links == nil {
			user.Links = make(map[string]UserLink)
		}
		user.Links[serverID] = link
		user.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (app *App) setServerStatus(serverID, status, lastError string) error {
	return app.store.Update(func(state *State) error {
		server, _ := findServer(state, serverID)
		if server == nil {
			return fmt.Errorf("server not found")
		}
		server.Status = status
		server.LastError = lastError
		server.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (app *App) forgetServer(serverID string) error {
	return app.store.Update(func(state *State) error {
		_, index := findServer(state, serverID)
		if index < 0 {
			return fmt.Errorf("server not found")
		}
		state.Servers = append(state.Servers[:index], state.Servers[index+1:]...)
		for userIndex := range state.Users {
			delete(state.Users[userIndex].Links, serverID)
			state.Users[userIndex].UpdatedAt = time.Now().UTC()
		}
		return nil
	})
}
