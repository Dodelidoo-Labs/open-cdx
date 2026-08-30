package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
)

func (store *Store) CreateEnrollment(ctx context.Context, name string) (Enrollment, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return Enrollment{}, errors.New("device name must contain 1 to 80 characters")
	}
	deviceID, err := secure.RandomURLSafe(18)
	if err != nil {
		return Enrollment{}, err
	}
	secret, err := secure.RandomURLSafe(32)
	if err != nil {
		return Enrollment{}, err
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO devices(id, name, status, enrollment_hash, created_at) VALUES(?,?, 'pending', ?, ?)`,
		deviceID, name, secure.Digest(secret), time.Now().Unix())
	if err != nil {
		return Enrollment{}, err
	}
	return Enrollment{DeviceID: deviceID, EnrollmentSecret: secret, Status: "pending"}, nil
}

func (store *Store) EnrollmentStatus(ctx context.Context, deviceID, enrollmentSecret string) (Enrollment, error) {
	var status string
	var expectedHash, credentialBlob []byte
	var acknowledged int
	err := store.db.QueryRowContext(ctx, `
		SELECT status, enrollment_hash, issued_credential_blob, credential_acknowledged
		FROM devices WHERE id=?`, deviceID).Scan(&status, &expectedHash, &credentialBlob, &acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return Enrollment{}, ErrNotFound
	}
	if err != nil {
		return Enrollment{}, err
	}
	if !secure.EqualDigest(expectedHash, enrollmentSecret) {
		return Enrollment{}, ErrNotFound
	}
	switch status {
	case "pending":
		return Enrollment{DeviceID: deviceID, Status: status}, ErrEnrollmentPending
	case "rejected", "revoked":
		return Enrollment{DeviceID: deviceID, Status: status}, ErrEnrollmentRejected
	case "approved":
		if acknowledged != 0 || len(credentialBlob) == 0 {
			return Enrollment{DeviceID: deviceID, Status: status}, ErrEnrollmentComplete
		}
		credential, openErr := store.box.Open(credentialBlob, []byte("device-issue:"+deviceID))
		if openErr != nil {
			return Enrollment{}, fmt.Errorf("decrypt issued device credential: %w", openErr)
		}
		return Enrollment{DeviceID: deviceID, Status: status, DeviceToken: string(credential)}, nil
	default:
		return Enrollment{}, errors.New("unknown device status")
	}
}

func (store *Store) AcknowledgeEnrollment(ctx context.Context, deviceID, enrollmentSecret string) error {
	var expectedHash []byte
	var status string
	err := store.db.QueryRowContext(ctx, `SELECT enrollment_hash, status FROM devices WHERE id=?`, deviceID).Scan(&expectedHash, &status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !secure.EqualDigest(expectedHash, enrollmentSecret)) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "approved" {
		return ErrEnrollmentPending
	}
	_, err = store.db.ExecContext(ctx, `UPDATE devices SET issued_credential_blob=NULL, credential_acknowledged=1 WHERE id=?`, deviceID)
	return err
}

func (store *Store) ApproveDevice(ctx context.Context, deviceID string) error {
	credential, err := secure.RandomURLSafe(36)
	if err != nil {
		return err
	}
	sealed, err := store.box.Seal([]byte(credential), []byte("device-issue:"+deviceID))
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `
		UPDATE devices SET status='approved', credential_hash=?, issued_credential_blob=?,
		credential_acknowledged=0, approved_at=?, revoked_at=0 WHERE id=? AND status='pending'`,
		secure.Digest(credential), sealed, time.Now().Unix(), deviceID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) RejectDevice(ctx context.Context, deviceID string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE devices SET status='rejected', issued_credential_blob=NULL WHERE id=? AND status='pending'`, deviceID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) RevokeDevice(ctx context.Context, deviceID string) error {
	result, err := store.db.ExecContext(ctx, `DELETE FROM devices WHERE id=? AND status='approved'`, deviceID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) DeleteDevice(ctx context.Context, deviceID string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var status string
	if err = transaction.QueryRowContext(ctx, `SELECT status FROM devices WHERE id=?`, deviceID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "rejected" {
		return ErrDeviceNotDeletable
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM devices WHERE id=? AND status='rejected'`, deviceID)
	if err != nil {
		return err
	}
	if err = requireChanged(result); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *Store) AuthenticateDevice(ctx context.Context, token string) (Device, error) {
	if strings.TrimSpace(token) == "" {
		return Device{}, ErrNotFound
	}
	var device Device
	var created, approved, seen, revoked, synced int64
	err := store.db.QueryRowContext(ctx, `
		SELECT id, name, status, created_at, approved_at, last_seen_at, revoked_at, catalog_synced_at
		FROM devices WHERE credential_hash=?`, secure.Digest(token)).Scan(
		&device.ID, &device.Name, &device.Status, &created, &approved, &seen, &revoked, &synced)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, err
	}
	if device.Status == "revoked" {
		return Device{}, ErrDeviceRevoked
	}
	if device.Status != "approved" {
		return Device{}, ErrEnrollmentPending
	}
	device.CreatedAt, device.ApprovedAt, device.LastSeenAt = fromUnix(created), fromUnix(approved), fromUnix(seen)
	device.RevokedAt, device.CatalogSynced = fromUnix(revoked), fromUnix(synced)
	now := time.Now().UTC()
	if device.LastSeenAt.IsZero() || now.Sub(device.LastSeenAt) >= time.Minute {
		_, _ = store.db.ExecContext(ctx, `UPDATE devices SET last_seen_at=? WHERE id=?`, now.Unix(), device.ID)
		device.LastSeenAt = now
	}
	return device, nil
}

func (store *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT id, name, status, created_at, approved_at, last_seen_at, revoked_at, catalog_synced_at
		FROM devices ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []Device
	for rows.Next() {
		var device Device
		var created, approved, seen, revoked, synced int64
		if err = rows.Scan(&device.ID, &device.Name, &device.Status, &created, &approved, &seen, &revoked, &synced); err != nil {
			return nil, err
		}
		device.CreatedAt, device.ApprovedAt, device.LastSeenAt = fromUnix(created), fromUnix(approved), fromUnix(seen)
		device.RevokedAt, device.CatalogSynced = fromUnix(revoked), fromUnix(synced)
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func (store *Store) MarkCatalogSynced(ctx context.Context, deviceID string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE devices SET catalog_synced_at=? WHERE id=?`, time.Now().Unix(), deviceID)
	return err
}
