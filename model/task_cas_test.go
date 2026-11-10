package model

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestMain(m *testing.M) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		panic("failed to open test db: " + err.Error())
	}
	DB = db
	LOG_DB = db

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	initCol()

	sqlDB, err := db.DB()
	if err != nil {
		panic("failed to get sql.DB: " + err.Error())
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(
		&Task{},
		&User{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
		&Token{},
		&PasskeyCredential{},
		&TwoFA{},
		&TwoFABackupCode{},
		&Log{},
		&Channel{},
		&QuotaData{},
		&Ability{},
		&TopUp{},
		&SubscriptionPlan{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&UserOAuthBinding{},
		&PerfMetric{},
		&SystemInstance{},
		&SystemTask{},
		&SystemTaskLock{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}

	os.Exit(m.Run())
}

func truncateTables(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		DB.Exec("DELETE FROM tasks")
		DB.Exec("DELETE FROM auth_flows")
		DB.Exec("DELETE FROM external_identity_claims")
		DB.Exec("DELETE FROM user_sessions")
		DB.Exec("DELETE FROM passkey_credentials")
		DB.Exec("DELETE FROM two_fa_backup_codes")
		DB.Exec("DELETE FROM two_fas")
		DB.Exec("DELETE FROM tokens")
		DB.Exec("DELETE FROM user_oauth_bindings")
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
		DB.Exec("DELETE FROM channels")
		DB.Exec("DELETE FROM quota_data")
		DB.Exec("DELETE FROM abilities")
		DB.Exec("DELETE FROM top_ups")
		DB.Exec("DELETE FROM subscription_orders")
		DB.Exec("DELETE FROM subscription_plans")
		DB.Exec("DELETE FROM user_subscriptions")
		DB.Exec("DELETE FROM perf_metrics")
		DB.Exec("DELETE FROM system_instances")
		DB.Exec("DELETE FROM system_task_locks")
		DB.Exec("DELETE FROM system_tasks")
	})
}

func insertTask(t *testing.T, task *Task) {
	t.Helper()
	task.CreatedAt = time.Now().Unix()
	task.UpdatedAt = time.Now().Unix()
	require.NoError(t, DB.Create(task).Error)
}

func testTaskBillingDatabases(t *testing.T, run func(*testing.T, *gorm.DB)) {
	t.Helper()
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var dialect gorm.Dialector
			switch engine {
			case "sqlite":
				dialect = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				dialect = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				dialect = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(dialect, &gorm.Config{DisableAutomaticPing: true, Logger: gormlogger.Default.LogMode(gormlogger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("tb%x_", time.Now().UnixNano())}})
			require.True(t, err == nil, "test database connection failed (details suppressed)")
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.True(t, sqlDB.Ping() == nil, "test database ping failed (details suppressed)")
			var version string
			query := "SELECT version()"
			if engine == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database: %s %s", engine, version)
			previous, previousType := DB, common.MainDatabaseType()
			DB = db
			switch engine {
			case "sqlite":
				common.SetMainDatabaseType(common.DatabaseTypeSQLite)
			case "mysql":
				common.SetMainDatabaseType(common.DatabaseTypeMySQL)
			case "postgres":
				common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
			}
			t.Cleanup(func() { DB = previous; common.SetMainDatabaseType(previousType) })
			run(t, db)
		})
	}
}

func TestTaskBillingConfirmationDatabase(t *testing.T) {
	testTaskBillingDatabases(t, func(t *testing.T, db *gorm.DB) {
		require.NoError(t, db.AutoMigrate(&Task{}))
		t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&Task{})) })
		task := &Task{TaskID: "billing", Quota: 5000, Status: TaskStatusSuccess, PrivateData: TaskPrivateData{UpstreamTaskID: "upstream", PluginState: json.RawMessage(`{"keep":true}`), BillingContext: &TaskBillingContext{ModelPrice: 1}}}
		require.NoError(t, db.Create(task).Error)
		for _, quota := range []int{3000, 0, 0} {
			require.NoError(t, task.ConfirmSettledQuota(quota))
			var stored Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			require.NotNil(t, stored.PrivateData.SettledQuota)
			assert.Equal(t, quota, *stored.PrivateData.SettledQuota)
			assert.Equal(t, quota, stored.Quota)
			assert.Equal(t, "upstream", stored.PrivateData.UpstreamTaskID)
			assert.JSONEq(t, `{"keep":true}`, string(stored.PrivateData.PluginState))
		}
		for _, confirm := range []bool{false, true} {
			// Hold a stale billing snapshot while a poller changes private state.
			var stale, latest Task
			require.NoError(t, db.First(&stale, task.ID).Error)
			require.NoError(t, db.First(&latest, task.ID).Error)
			latest.PrivateData.ResponsesBackground = true
			latest.PrivateData.BillingContext.ModelPrice = 2
			latest.PrivateData.PluginState = json.RawMessage(`{"round":2}`)
			latest.PrivateData.PollFailures = 4
			latest.PrivateData.ResultURL = "https://example.invalid/new-result"
			require.NoError(t, db.Model(&Task{}).Where("id = ?", task.ID).Update("private_data", latest.PrivateData).Error)
			stale.Quota = 125
			if confirm {
				require.NoError(t, stale.ConfirmSettledQuota(125))
			} else {
				stale.PrivateData.BillingAdjustmentFailed = true
				require.NoError(t, stale.UpdateQuota())
			}
			var stored Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			assert.True(t, stored.PrivateData.ResponsesBackground)
			assert.JSONEq(t, `{"round":2}`, string(stored.PrivateData.PluginState))
			assert.Equal(t, 4, stored.PrivateData.PollFailures)
			assert.Equal(t, latest.PrivateData.ResultURL, stored.PrivateData.ResultURL)
			assert.Equal(t, 2.0, stored.PrivateData.BillingContext.ModelPrice)
			assert.Equal(t, 125, stored.Quota)
			assert.Equal(t, !confirm, stored.PrivateData.BillingAdjustmentFailed)
			// Reset only the non-billing state to make both interleavings stale.
			stored.PrivateData.ResponsesBackground = false
			stored.PrivateData.PluginState = json.RawMessage(`{"round":1}`)
			require.NoError(t, db.Model(&Task{}).Where("id = ?", task.ID).Update("private_data", stored.PrivateData).Error)
		}
		zero := TaskPrivateData{SettledQuota: common.GetPointer(0)}
		value, err := zero.Value()
		require.NoError(t, err)
		assert.JSONEq(t, `{"settled_quota":0}`, value.(string))
		require.NoError(t, zero.Scan(nil))
		assert.Nil(t, zero.SettledQuota)
		task.ID = 999999999
		require.Error(t, task.ConfirmSettledQuota(0))
		require.Error(t, task.UpdateQuota())
	})
}

func TestVideoSubmissionBillingConfirmationDatabase(t *testing.T) {
	testTaskBillingDatabases(t, func(t *testing.T, db *gorm.DB) {
		require.NoError(t, db.AutoMigrate(&Task{}, &User{}, &Token{}, &Channel{}, &UserSubscription{}))
		t.Cleanup(func() {
			require.NoError(t, db.Migrator().DropTable(&Task{}, &User{}, &Token{}, &Channel{}, &UserSubscription{}))
		})
		oldBatch := common.BatchUpdateEnabled
		common.BatchUpdateEnabled = true // Durable video settlement does not use the batch queue.
		t.Cleanup(func() { common.BatchUpdateEnabled = oldBatch })
		for i, tc := range []struct {
			name         string
			status       TaskStatus
			target       int
			missingToken bool
			subscription bool
		}{
			{"success exact", TaskStatusSuccess, 100, false, false},
			{"success delta", TaskStatusSuccess, 150, false, false},
			{"failure refund", TaskStatusFailure, 0, false, false},
			{"pending exact", TaskStatusSubmitted, 100, false, false},
			{"pending delta", TaskStatusSubmitted, 150, false, false},
			{"deleted token refund", TaskStatusFailure, 0, true, false},
			{"subscription refund", TaskStatusFailure, 0, false, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				user := User{Username: fmt.Sprintf("video-billing-%d", i), AffCode: fmt.Sprintf("video-billing-%d", i), Quota: 900, UsedQuota: 100, RequestCount: 1}
				require.NoError(t, db.Create(&user).Error)
				token := Token{UserId: user.Id, Key: fmt.Sprintf("video-billing-%d", i), RemainQuota: 900, UsedQuota: 100}
				require.NoError(t, db.Create(&token).Error)
				channel := Channel{Name: tc.name, UsedQuota: 100}
				require.NoError(t, db.Create(&channel).Error)
				task := Task{TaskID: tc.name, UserId: user.Id, ChannelId: channel.Id, Quota: 100, Status: tc.status, SubmissionStage: "settling", SubmissionOwner: "worker", SubmissionLeaseUntil: common.GetTimestamp() + 60, PrivateData: TaskPrivateData{TokenId: token.Id, BillingAdjustmentFailed: true, ResponsesBackground: true, PluginState: json.RawMessage(`{"keep":true}`), VideoSubmission: &VideoSubmission{Accounted: true}}}
				if tc.missingToken {
					require.NoError(t, db.Delete(&token).Error)
				}
				sub := UserSubscription{UserId: user.Id, AmountTotal: 1000, AmountUsed: 100}
				if tc.subscription {
					require.NoError(t, db.Create(&sub).Error)
					task.PrivateData.BillingSource = "subscription"
					task.PrivateData.SubscriptionId = sub.Id
				}
				require.NoError(t, db.Create(&task).Error)
				if tc.name == "success delta" {
					// Fail the final write after wallet/token updates: the transaction
					// must roll back both ledgers and publish no confirmation.
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("fail_video_confirmation", func(tx *gorm.DB) {
						if _, ok := tx.Statement.Model.(*Task); ok {
							tx.AddError(fmt.Errorf("forced video confirmation failure"))
						}
					}))
					_, applied, err := AdjustVideoSubmissionQuota(t.Context(), task.ID, "worker", tc.target)
					require.ErrorContains(t, err, "forced video confirmation failure")
					assert.False(t, applied)
					require.NoError(t, db.Callback().Update().Remove("fail_video_confirmation"))
					var rolledBack Task
					require.NoError(t, db.First(&rolledBack, task.ID).Error)
					assert.Nil(t, rolledBack.PrivateData.SettledQuota)
					assert.Equal(t, 100, rolledBack.Quota)
					require.NoError(t, db.First(&user, user.Id).Error)
					require.NoError(t, db.First(&token, token.Id).Error)
					assert.Equal(t, 900, user.Quota)
					assert.Equal(t, 900, token.RemainQuota)
					assert.Equal(t, 100, token.UsedQuota)
				}
				previous, applied, err := AdjustVideoSubmissionQuota(t.Context(), task.ID, "wrong-owner", tc.target)
				require.NoError(t, err)
				assert.False(t, applied)
				assert.Equal(t, 100, previous)
				for range 2 {
					_, applied, err = AdjustVideoSubmissionQuota(t.Context(), task.ID, "worker", tc.target)
					require.NoError(t, err)
					require.True(t, applied)
				}
				var stored Task
				require.NoError(t, db.First(&stored, task.ID).Error)
				assert.Equal(t, tc.target, stored.Quota)
				if (tc.status == TaskStatusSuccess || tc.status == TaskStatusFailure) && !tc.missingToken {
					require.NotNil(t, stored.PrivateData.SettledQuota)
					assert.Equal(t, tc.target, *stored.PrivateData.SettledQuota)
					assert.False(t, stored.PrivateData.BillingAdjustmentFailed)
				} else {
					assert.Nil(t, stored.PrivateData.SettledQuota)
				}
				assert.True(t, stored.PrivateData.ResponsesBackground)
				assert.JSONEq(t, `{"keep":true}`, string(stored.PrivateData.PluginState))
				require.NoError(t, db.First(&user, user.Id).Error)
				wantWallet := 1000 - tc.target
				if tc.subscription {
					wantWallet = 900
					require.NoError(t, db.First(&sub, sub.Id).Error)
					assert.EqualValues(t, tc.target, sub.AmountUsed)
				}
				assert.Equal(t, wantWallet, user.Quota)
				assert.Equal(t, tc.target, user.UsedQuota)
				assert.Equal(t, 1, user.RequestCount)
				require.NoError(t, db.First(&channel, channel.Id).Error)
				assert.EqualValues(t, tc.target, channel.UsedQuota)
				if !tc.missingToken {
					require.NoError(t, db.First(&token, token.Id).Error)
					assert.Equal(t, 1000-tc.target, token.RemainQuota)
					assert.Equal(t, tc.target, token.UsedQuota)
				}
			})
		}
	})
}

func TestGetTaskForProtocolObservationScopesOwnerAndPlatform(t *testing.T) {
	truncateTables(t)
	task := &Task{
		TaskID:   "task_protocol_scope",
		UserId:   7,
		Platform: "plugin-a",
		Status:   TaskStatusInProgress,
	}
	insertTask(t, task)

	got, exists, err := GetTaskForProtocolObservation(context.Background(), 7, "plugin-a", task.TaskID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, task.ID, got.ID)

	for _, query := range []struct {
		userID   int
		platform string
	}{
		{userID: 8, platform: "plugin-a"},
		{userID: 7, platform: "plugin-b"},
	} {
		got, exists, err = GetTaskForProtocolObservation(context.Background(), query.userID, constant.TaskPlatform(query.platform), task.TaskID)
		require.NoError(t, err)
		assert.False(t, exists)
		assert.Nil(t, got)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = GetTaskForProtocolObservation(cancelled, 7, "plugin-a", task.TaskID)
	require.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// Snapshot / Equal — pure logic tests (no DB)
// ---------------------------------------------------------------------------

func TestSnapshotEqual_Same(t *testing.T) {
	s := taskSnapshot{
		Status:     TaskStatusInProgress,
		Progress:   "50%",
		StartTime:  1000,
		FinishTime: 0,
		FailReason: "",
		ResultURL:  "",
		Data:       json.RawMessage(`{"key":"value"}`),
	}
	assert.True(t, s.Equal(s))
}

func TestSnapshotEqual_DifferentStatus(t *testing.T) {
	a := taskSnapshot{Status: TaskStatusInProgress, Data: json.RawMessage(`{}`)}
	b := taskSnapshot{Status: TaskStatusSuccess, Data: json.RawMessage(`{}`)}
	assert.False(t, a.Equal(b))
}

func TestSnapshotEqual_DifferentProgress(t *testing.T) {
	a := taskSnapshot{Status: TaskStatusInProgress, Progress: "30%", Data: json.RawMessage(`{}`)}
	b := taskSnapshot{Status: TaskStatusInProgress, Progress: "60%", Data: json.RawMessage(`{}`)}
	assert.False(t, a.Equal(b))
}

func TestSnapshotEqual_DifferentData(t *testing.T) {
	a := taskSnapshot{Status: TaskStatusInProgress, Data: json.RawMessage(`{"a":1}`)}
	b := taskSnapshot{Status: TaskStatusInProgress, Data: json.RawMessage(`{"a":2}`)}
	assert.False(t, a.Equal(b))
}

func TestSnapshotEqual_NilVsEmpty(t *testing.T) {
	a := taskSnapshot{Status: TaskStatusInProgress, Data: nil}
	b := taskSnapshot{Status: TaskStatusInProgress, Data: json.RawMessage{}}
	// bytes.Equal(nil, []byte{}) == true
	assert.True(t, a.Equal(b))
}

func TestSnapshotEqual_PluginStateAndPollFailures(t *testing.T) {
	base := taskSnapshot{
		Status:       TaskStatusInProgress,
		PluginState:  json.RawMessage(`{"req_key":"a"}`),
		PollFailures: 2,
	}
	assert.True(t, base.Equal(taskSnapshot{
		Status:       TaskStatusInProgress,
		PluginState:  json.RawMessage(`{"req_key":"a"}`),
		PollFailures: 2,
	}))
	assert.False(t, base.Equal(taskSnapshot{
		Status:       TaskStatusInProgress,
		PluginState:  json.RawMessage(`{"req_key":"b"}`),
		PollFailures: 2,
	}))
	assert.False(t, base.Equal(taskSnapshot{
		Status:       TaskStatusInProgress,
		PluginState:  json.RawMessage(`{"req_key":"a"}`),
		PollFailures: 3,
	}))
}

func TestSnapshot_Roundtrip(t *testing.T) {
	task := &Task{
		Status:     TaskStatusInProgress,
		Progress:   "42%",
		StartTime:  1234,
		FinishTime: 5678,
		FailReason: "timeout",
		PrivateData: TaskPrivateData{
			ResultURL:    "https://example.com/result.mp4",
			PluginState:  json.RawMessage(`{"req_key":"keep"}`),
			PollFailures: 3,
		},
		Data: json.RawMessage(`{"model":"test-model"}`),
	}
	snap := task.Snapshot()
	assert.Equal(t, task.Status, snap.Status)
	assert.Equal(t, task.Progress, snap.Progress)
	assert.Equal(t, task.StartTime, snap.StartTime)
	assert.Equal(t, task.FinishTime, snap.FinishTime)
	assert.Equal(t, task.FailReason, snap.FailReason)
	assert.Equal(t, task.PrivateData.ResultURL, snap.ResultURL)
	assert.JSONEq(t, string(task.Data), string(snap.Data))
	assert.Equal(t, task.PrivateData.PluginState, snap.PluginState)
	assert.Equal(t, task.PrivateData.PollFailures, snap.PollFailures)
}

// ---------------------------------------------------------------------------
// UpdateWithStatus CAS — DB integration tests
// ---------------------------------------------------------------------------

func TestUpdateWithStatus_Win(t *testing.T) {
	truncateTables(t)

	task := &Task{
		TaskID:   "task_cas_win",
		Status:   TaskStatusInProgress,
		Progress: "50%",
		Data:     json.RawMessage(`{}`),
	}
	insertTask(t, task)

	task.Status = TaskStatusSuccess
	task.Progress = "100%"
	won, err := task.UpdateWithStatus(TaskStatusInProgress)
	require.NoError(t, err)
	assert.True(t, won)

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusSuccess, reloaded.Status)
	assert.Equal(t, "100%", reloaded.Progress)
}

func TestUpdateWithStatus_Lose(t *testing.T) {
	truncateTables(t)

	task := &Task{
		TaskID: "task_cas_lose",
		Status: TaskStatusFailure,
		Data:   json.RawMessage(`{}`),
	}
	insertTask(t, task)

	task.Status = TaskStatusSuccess
	won, err := task.UpdateWithStatus(TaskStatusInProgress) // wrong fromStatus
	require.NoError(t, err)
	assert.False(t, won)

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusFailure, reloaded.Status) // unchanged
}

func TestUpdateWithStatus_ConcurrentWinner(t *testing.T) {
	truncateTables(t)

	task := &Task{
		TaskID: "task_cas_race",
		Status: TaskStatusInProgress,
		Quota:  1000,
		Data:   json.RawMessage(`{}`),
	}
	insertTask(t, task)

	const goroutines = 5
	wins := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			t := &Task{}
			*t = Task{
				ID:       task.ID,
				TaskID:   task.TaskID,
				Status:   TaskStatusSuccess,
				Progress: "100%",
				Quota:    task.Quota,
				Data:     json.RawMessage(`{}`),
			}
			t.CreatedAt = task.CreatedAt
			t.UpdatedAt = time.Now().Unix()
			won, err := t.UpdateWithStatus(TaskStatusInProgress)
			if err == nil {
				wins[idx] = won
			}
		}(i)
	}
	wg.Wait()

	winCount := 0
	for _, w := range wins {
		if w {
			winCount++
		}
	}
	assert.Equal(t, 1, winCount, "exactly one goroutine should win the CAS")
}

func TestUpdateWithStatus_PersistsPluginStateAndPollFailures(t *testing.T) {
	truncateTables(t)

	task := &Task{
		TaskID: "task_cas_plugin_state",
		Status: TaskStatusInProgress,
		Data:   json.RawMessage(`{}`),
		PrivateData: TaskPrivateData{
			PluginState:  json.RawMessage(`{"req_key":"old"}`),
			PollFailures: 1,
		},
	}
	insertTask(t, task)

	task.PrivateData.PluginState = json.RawMessage(`{"req_key":"new"}`)
	task.PrivateData.PollFailures = 4
	won, err := task.UpdateWithStatus(TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)

	var reloaded Task
	require.NoError(t, DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, TaskStatusInProgress, reloaded.Status)
	assert.JSONEq(t, `{"req_key":"new"}`, string(reloaded.PrivateData.PluginState))
	assert.Equal(t, 4, reloaded.PrivateData.PollFailures)
}
