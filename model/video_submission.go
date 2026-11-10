package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrVideoSubmissionQuota = errors.New("insufficient quota for video task")

func ReserveVideoSubmission(ctx context.Context, task *Task, preference string, free, playground bool) error {
	if task.PrivateData.VideoSubmission == nil || task.Quota < 0 {
		return errors.New("invalid video submission reservation")
	}
	walletCached, tokenCached := false, false
	var cachedWalletQuota *int
	var tokenKey string
	if free {
		task.Quota = 0
		task.PrivateData.VideoSubmission.TargetQuota = 0
	}
	if common.RedisEnabled && task.Quota > 0 && !free {
		user, err := GetUserCache(task.UserId)
		if err != nil {
			return err
		}
		cachedWalletQuota = &user.Quota
		if !playground && task.PrivateData.TokenId > 0 {
			if token, err := GetTokenById(task.PrivateData.TokenId); err == nil {
				tokenKey = token.Key
				if _, err := cacheInitToken(*token); err != nil {
					return err
				}
			}
		}
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, task.UserId).Error; err != nil {
			return err
		}
		task.PrivateData.BillingSource = "wallet"
		task.PrivateData.SubscriptionId = 0
		wallet := !free && task.Quota > 0
		if wallet && preference != "wallet_only" {
			var subs []UserSubscription
			if err := lockForUpdate(tx).Where("user_id = ? AND status = ? AND end_time > ?", task.UserId, "active", common.GetTimestamp()).Order("end_time asc, id asc").Find(&subs).Error; err != nil {
				return err
			}
			walletQuota := user.Quota
			if cachedWalletQuota != nil {
				walletQuota = *cachedWalletQuota
			}
			useSubscription := preference != "wallet_first" || walletQuota < task.Quota
			allowWallet := preference != "subscription_only"
			for i := range subs {
				if !subs[i].AllowWalletOverflow {
					allowWallet = false
				}
			}
			if useSubscription {
				for i := range subs {
					sub := &subs[i]
					plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
					if err != nil {
						return err
					}
					if err := maybeResetUserSubscriptionWithPlanTx(tx, sub, plan, common.GetTimestamp()); err != nil {
						return err
					}
					if sub.AmountTotal > 0 && sub.AmountTotal-sub.AmountUsed < int64(task.Quota) {
						continue
					}
					if err := tx.Model(sub).Update("amount_used", gorm.Expr("amount_used + ?", task.Quota)).Error; err != nil {
						return err
					}
					task.PrivateData.BillingSource, task.PrivateData.SubscriptionId = "subscription", sub.Id
					wallet = false
					break
				}
				if wallet && !allowWallet {
					return ErrVideoSubmissionQuota
				}
			}
		}
		if wallet {
			if common.RedisEnabled {
				result, err := cacheTryReserveUserQuota(task.UserId, int64(task.Quota))
				if err != nil {
					return err
				}
				if result == cacheQuotaInsufficient {
					return ErrVideoSubmissionQuota
				}
				walletCached = result == cacheQuotaOK
			}
			if !walletCached && user.Quota < task.Quota {
				return ErrVideoSubmissionQuota
			}
			if err := tx.Model(&user).Update("quota", gorm.Expr("quota - ?", task.Quota)).Error; err != nil {
				return err
			}
		}
		if !free && task.Quota > 0 && !playground {
			var token Token
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", task.PrivateData.TokenId, task.UserId).First(&token).Error; err != nil {
				return err
			}
			tokenKey = token.Key
			if common.RedisEnabled && !token.UnlimitedQuota {
				result, err := cacheTryReserveTokenQuota(token.Id, tokenKey, int64(task.Quota))
				if err != nil {
					return err
				}
				if result == cacheQuotaInsufficient {
					return ErrVideoSubmissionQuota
				}
				tokenCached = result == cacheQuotaOK
			}
			if !token.UnlimitedQuota && !tokenCached && token.RemainQuota < task.Quota {
				return ErrVideoSubmissionQuota
			}
			if err := tx.Model(&token).Updates(map[string]any{"remain_quota": gorm.Expr("remain_quota - ?", task.Quota), "used_quota": gorm.Expr("used_quota + ?", task.Quota), "accessed_time": common.GetTimestamp()}).Error; err != nil {
				return err
			}
		}
		if playground || free {
			task.PrivateData.TokenId = 0
		}
		task.PrivateData.VideoSubmission.Accounted = true
		task.SubmissionStage, task.SubmissionLeaseUntil = "pending", 0
		if err := task.insertWithDB(tx); err != nil {
			return err
		}
		if err := tx.Model(&user).Updates(map[string]any{"used_quota": gorm.Expr("used_quota + ?", task.Quota), "request_count": gorm.Expr("request_count + 1")}).Error; err != nil {
			return err
		}
		return tx.Model(&Channel{}).Where("id = ?", task.ChannelId).Update("used_quota", gorm.Expr("used_quota + ?", task.Quota)).Error
	})
	if err != nil {
		if walletCached {
			_, _ = cacheApplyUserQuotaDelta(task.UserId, int64(task.Quota))
		}
		if tokenCached {
			_, _ = cacheApplyTokenQuotaDelta(task.PrivateData.TokenId, tokenKey, int64(task.Quota))
		}
		return err
	}
	if common.RedisEnabled && tokenKey != "" && !tokenCached && !free && task.Quota > 0 {
		_, _ = cacheApplyTokenQuotaDelta(task.PrivateData.TokenId, tokenKey, -int64(task.Quota))
	}
	return nil
}

func AdjustVideoSubmissionQuota(ctx context.Context, taskID int64, owner string, targetQuota int) (previousQuota int, applied bool, err error) {
	if taskID <= 0 || strings.TrimSpace(owner) == "" {
		return 0, false, errors.New("invalid video submission task or owner")
	}
	if targetQuota < 0 {
		return 0, false, errors.New("video submission target quota is negative")
	}

	var userID int
	var tokenKey string
	var tokenID int
	var cacheDelta int64
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task Task
		if err := lockForUpdate(tx).
			Select("id", "user_id", "channel_id", "quota", "private_data", "status", "submission_stage", "submission_owner", "submission_lease_until").
			Where("id = ?", taskID).First(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		previousQuota = task.Quota
		if task.SubmissionOwner != owner || task.SubmissionLeaseUntil < common.GetTimestamp() ||
			(task.SubmissionStage != "settling" && task.SubmissionStage != "failed") {
			return nil
		}
		userID = task.UserId
		if task.Quota < 0 {
			return errors.New("video submission stored quota is negative")
		}
		terminal := task.Status == TaskStatusSuccess || task.Status == TaskStatusFailure
		if task.Quota == targetQuota {
			// The reservation itself was transactional, so no second money move
			// is needed. A deleted token still leaves its accounting uncertain.
			confirmed := terminal
			if confirmed && task.PrivateData.TokenId > 0 {
				var token Token
				tokenErr := lockForUpdate(tx).Select("id", "user_id").Where("id = ?", task.PrivateData.TokenId).First(&token).Error
				if tokenErr != nil && !errors.Is(tokenErr, gorm.ErrRecordNotFound) {
					return tokenErr
				}
				confirmed = tokenErr == nil && token.UserId == userID
			}
			if confirmed {
				task.PrivateData.SettledQuota = &targetQuota
				task.PrivateData.BillingAdjustmentFailed = false
				if err := tx.Model(&Task{}).Where("id = ?", taskID).Update("private_data", task.PrivateData).Error; err != nil {
					return err
				}
			}
			applied = true
			return nil
		}

		var user User
		if err := lockForUpdate(tx).Select("id", "quota", "used_quota").Where("id = ?", userID).First(&user).Error; err != nil {
			return err
		}
		delta := int64(targetQuota) - int64(task.Quota)
		wallet := false
		switch task.PrivateData.BillingSource {
		case "", "wallet":
			wallet = true
		case "subscription":
			if task.PrivateData.SubscriptionId <= 0 {
				return errors.New("invalid video submission subscription id")
			}
			var sub UserSubscription
			if err := lockForUpdate(tx).Where("id = ?", task.PrivateData.SubscriptionId).First(&sub).Error; err != nil {
				return err
			}
			if sub.UserId != userID {
				return errors.New("video submission subscription user mismatch")
			}
			newUsed, err := videoSubmissionQuotaAfterDelta(sub.AmountUsed, delta)
			if err != nil {
				return err
			}
			newUsed = max(newUsed, 0)
			if sub.AmountTotal > 0 && newUsed > sub.AmountTotal {
				return fmt.Errorf("subscription used exceeds total, used=%d total=%d", newUsed, sub.AmountTotal)
			}
			if err := tx.Model(&sub).Update("amount_used", newUsed).Error; err != nil {
				return err
			}
		default:
			return errors.New("unsupported video submission billing source")
		}

		if _, err := videoSubmissionQuotaAfterDelta(int64(user.UsedQuota), delta); err != nil {
			return err
		}
		accounted := task.PrivateData.VideoSubmission != nil && task.PrivateData.VideoSubmission.Accounted
		userUpdates := make(map[string]any)
		if accounted {
			userUpdates["used_quota"] = gorm.Expr("used_quota + ?", delta)
		}
		if wallet {
			newQuota, err := videoSubmissionQuotaAfterDelta(int64(user.Quota), -delta)
			if err != nil {
				return err
			}
			if newQuota > common.MaxWalletQuota {
				return ErrWalletQuotaLimitExceeded
			}
			userUpdates["quota"] = gorm.Expr("quota - ?", delta)
		}
		result := tx.Model(&User{}).Where("id = ?", userID).Updates(userUpdates)
		if result.Error != nil {
			return result.Error
		}

		tokenAdjusted := task.PrivateData.TokenId <= 0
		if task.PrivateData.TokenId > 0 {
			var token Token
			tokenErr := lockForUpdate(tx).
				Select("id", "user_id", "key", "remain_quota", "used_quota", "unlimited_quota").
				Where("id = ?", task.PrivateData.TokenId).First(&token).Error
			if tokenErr != nil && !errors.Is(tokenErr, gorm.ErrRecordNotFound) {
				return tokenErr
			}
			if tokenErr == nil {
				tokenKey, tokenID = token.Key, token.Id
				if token.UserId != userID {
					return errors.New("video submission token user mismatch")
				}
				if _, err := videoSubmissionQuotaAfterDelta(int64(token.RemainQuota), -delta); err != nil {
					return err
				}
				if _, err := videoSubmissionQuotaAfterDelta(int64(token.UsedQuota), delta); err != nil {
					return err
				}
				result := tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{
					"remain_quota":  gorm.Expr("remain_quota - ?", delta),
					"used_quota":    gorm.Expr("used_quota + ?", delta),
					"accessed_time": common.GetTimestamp(),
				})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return gorm.ErrRecordNotFound
				}
				tokenAdjusted = true
			}
		}

		if accounted {
			var channel Channel
			channelErr := lockForUpdate(tx).Select("id", "used_quota").Where("id = ?", task.ChannelId).First(&channel).Error
			if channelErr != nil && !errors.Is(channelErr, gorm.ErrRecordNotFound) {
				return channelErr
			}
			if channelErr == nil {
				if _, err := videoSubmissionQuotaAfterDelta(channel.UsedQuota, delta); err != nil {
					return err
				}
				if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error; err != nil {
					return err
				}
			}
		}

		updates := map[string]any{"quota": targetQuota}
		if terminal {
			task.PrivateData.SettledQuota = nil
			task.PrivateData.BillingAdjustmentFailed = !tokenAdjusted
			if tokenAdjusted {
				task.PrivateData.SettledQuota = &targetQuota
			}
			updates["private_data"] = task.PrivateData
		}
		result = tx.Model(&Task{}).
			Where("id = ? AND quota = ? AND submission_owner = ? AND submission_stage = ? AND submission_lease_until >= ?",
				taskID, previousQuota, owner, task.SubmissionStage, common.GetTimestamp()).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("video submission lease lost during quota adjustment")
		}
		if wallet {
			cacheDelta = -delta
		}
		applied = true
		return nil
	})
	if err != nil {
		return previousQuota, false, err
	}
	if !applied || !common.RedisEnabled {
		return previousQuota, applied, nil
	}
	if cacheDelta != 0 {
		if _, cacheErr := cacheApplyUserQuotaDelta(userID, cacheDelta); cacheErr != nil {
			common.SysError(fmt.Sprintf("video submission user cache adjustment failed (task_id=%d, user_id=%d)", taskID, userID))
		}
	}
	if tokenKey != "" && previousQuota != targetQuota {
		if _, cacheErr := cacheApplyTokenQuotaDelta(tokenID, tokenKey, int64(previousQuota)-int64(targetQuota)); cacheErr != nil {
			common.SysError(fmt.Sprintf("video submission token cache adjustment failed (task_id=%d)", taskID))
		}
	}
	return previousQuota, true, nil
}

func videoSubmissionQuotaAfterDelta(quota, delta int64) (int64, error) {
	if (delta > 0 && quota > math.MaxInt64-delta) || (delta < 0 && quota < math.MinInt64-delta) {
		return 0, errors.New("video submission quota adjustment overflows accounting range")
	}
	return quota + delta, nil
}
