package controller

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// publicMaterialURL is an IP-literal public URL so SSRF validation resolves
// without DNS during tests.
const publicMaterialURL = "https://8.8.8.8/materials/a.png"

func setupMaterialTestDB(t *testing.T, tables ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, sqlDB.Close())
	})
	if len(tables) == 0 {
		tables = []any{&model.User{}, &model.Material{}, &model.MaterialGroup{}}
	}
	require.NoError(t, db.AutoMigrate(tables...))
	return db
}

func newMaterialContext(recorder *httptest.ResponseRecorder, method, target, body string, userID int) *gin.Context {
	c, _ := gin.CreateTestContext(recorder)
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	c.Request = httptest.NewRequest(method, target, reader)
	if body != "" {
		c.Request.Header.Set("Content-Type", "application/json")
	}
	c.Set("id", userID)
	return c
}

type materialEnvelope struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Data    model.Material `json:"data"`
}

type materialListEnvelope struct {
	Success bool             `json:"success"`
	Total   int64            `json:"total"`
	Data    []model.Material `json:"data"`
}

type materialBoolEnvelope struct {
	Success bool `json:"success"`
	Data    bool `json:"data"`
}

func TestAddMaterialCreatesPendingVirtualRecord(t *testing.T) {
	setupMaterialTestDB(t)
	recorder := httptest.NewRecorder()
	body := `{"name":"hero clip","library_type":"AIGC","file_type":"Image","source_url":"` + publicMaterialURL + `"}`
	c := newMaterialContext(recorder, http.MethodPost, "/api/material/", body, 7)

	AddMaterial(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope materialEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.True(t, envelope.Success, recorder.Body.String())
	assert.True(t, strings.HasPrefix(envelope.Data.MaterialID, "mat_"))
	assert.Equal(t, model.MaterialLibraryAIGC, envelope.Data.LibraryType)
	assert.Equal(t, model.MaterialFileImage, envelope.Data.FileType)
	assert.Equal(t, model.MaterialStatusPending, envelope.Data.Status)
	assert.Equal(t, 7, envelope.Data.UserId)

	var count int64
	require.NoError(t, model.DB.Model(&model.Material{}).Where("user_id = ?", 7).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAddMaterialRejectsRealPersonAndInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"real person pending", `{"name":"face","library_type":"LivenessFace","file_type":"Image","source_url":"` + publicMaterialURL + `"}`},
		{"missing name", `{"library_type":"AIGC","file_type":"Image","source_url":"` + publicMaterialURL + `"}`},
		{"invalid file type", `{"name":"x","library_type":"AIGC","file_type":"Doc","source_url":"` + publicMaterialURL + `"}`},
		{"missing url", `{"name":"x","library_type":"AIGC","file_type":"Image"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupMaterialTestDB(t)
			recorder := httptest.NewRecorder()
			c := newMaterialContext(recorder, http.MethodPost, "/api/material/", tc.body, 7)

			AddMaterial(c)

			var envelope materialEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
			assert.False(t, envelope.Success, recorder.Body.String())
			var count int64
			require.NoError(t, model.DB.Model(&model.Material{}).Count(&count).Error)
			assert.EqualValues(t, 0, count)
		})
	}
}

func TestListMaterialsIsolatesByUser(t *testing.T) {
	setupMaterialTestDB(t)
	now := common.GetTimestamp()
	require.NoError(t, model.DB.Create(&[]model.Material{
		{MaterialID: "mat_a1", UserId: 1, Name: "a1", LibraryType: model.MaterialLibraryAIGC, FileType: model.MaterialFileImage, Status: model.MaterialStatusPending, CreatedTime: now, UpdatedTime: now},
		{MaterialID: "mat_a2", UserId: 1, Name: "a2", LibraryType: model.MaterialLibraryAIGC, FileType: model.MaterialFileVideo, Status: model.MaterialStatusReady, CreatedTime: now, UpdatedTime: now},
		{MaterialID: "mat_b1", UserId: 2, Name: "b1", LibraryType: model.MaterialLibraryAIGC, FileType: model.MaterialFileImage, Status: model.MaterialStatusPending, CreatedTime: now, UpdatedTime: now},
	}).Error)

	recorder := httptest.NewRecorder()
	c := newMaterialContext(recorder, http.MethodGet, "/api/material/list", "", 1)
	ListMaterials(c)
	var userView materialListEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &userView))
	assert.True(t, userView.Success)
	assert.EqualValues(t, 2, userView.Total)
	for _, material := range userView.Data {
		assert.Equal(t, 1, material.UserId)
	}

	recorder = httptest.NewRecorder()
	c = newMaterialContext(recorder, http.MethodGet, "/api/material/all", "", 99)
	ListAllMaterials(c)
	var adminView materialListEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &adminView))
	assert.True(t, adminView.Success)
	assert.EqualValues(t, 3, adminView.Total)
}

func TestListMaterialsRejectsInvalidFilters(t *testing.T) {
	setupMaterialTestDB(t)
	for _, target := range []string{
		"/api/material/list?library_type=Unknown",
		"/api/material/list?group_id=-1",
		"/api/material/list?group_id=not-a-number",
		"/api/material/list?offset=-1",
		"/api/material/list?limit=0",
		"/api/material/list?limit=1000",
	} {
		t.Run(target, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c := newMaterialContext(recorder, http.MethodGet, target, "", 1)
			ListMaterials(c)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestGetAndDeleteMaterialEnforceOwnership(t *testing.T) {
	setupMaterialTestDB(t)
	now := common.GetTimestamp()
	require.NoError(t, model.DB.Create(&model.Material{
		Id: 1, MaterialID: "mat_b1", UserId: 2, Name: "b1", LibraryType: model.MaterialLibraryAIGC,
		FileType: model.MaterialFileImage, Status: model.MaterialStatusPending, CreatedTime: now, UpdatedTime: now,
	}).Error)

	recorder := httptest.NewRecorder()
	c := newMaterialContext(recorder, http.MethodGet, "/api/material/1", "", 1)
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	GetMaterial(c)
	var envelope materialEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.False(t, envelope.Success, "user must not read another user's material")

	recorder = httptest.NewRecorder()
	c = newMaterialContext(recorder, http.MethodDelete, "/api/material/1", "", 1)
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	DeleteMaterial(c)
	var deleteEnvelope materialEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &deleteEnvelope))
	assert.False(t, deleteEnvelope.Success, "user must not delete another user's material")

	var count int64
	require.NoError(t, model.DB.Model(&model.Material{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestMaterialGroupsAndAssignment(t *testing.T) {
	setupMaterialTestDB(t)
	create := func(userID int, name string) materialGroupEnvelope {
		t.Helper()
		body, err := common.Marshal(map[string]string{"name": name})
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		CreateMaterialGroup(newMaterialContext(recorder, http.MethodPost, "/api/material/groups", string(body), userID))
		var envelope materialGroupEnvelope
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
		return envelope
	}
	own := create(7, "  Portraits  ")
	require.True(t, own.Success)
	assert.Equal(t, "Portraits", own.Data.Name)
	assert.False(t, create(7, "portraits").Success)
	foreign := create(8, "Portraits")
	require.True(t, foreign.Success)
	assert.False(t, create(7, "   ").Success)
	assert.False(t, create(7, strings.Repeat("图", 65)).Success)

	recorder := httptest.NewRecorder()
	ListMaterialGroups(newMaterialContext(recorder, http.MethodGet, "/api/material/groups", "", 7))
	var groups struct {
		Success bool                  `json:"success"`
		Data    []model.MaterialGroup `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &groups))
	require.True(t, groups.Success)
	require.Len(t, groups.Data, 1)
	assert.Equal(t, own.Data.Id, groups.Data[0].Id)

	for _, tc := range []struct {
		name    string
		groupID int
		success bool
	}{
		{"own group", own.Data.Id, true},
		{"foreign group", foreign.Data.Id, false},
		{"missing group", 9999, false},
		{"invalid group", -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{"name": tc.name, "file_type": "Image", "source_url": publicMaterialURL, "group_id": tc.groupID})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			AddMaterial(newMaterialContext(recorder, http.MethodPost, "/api/material/", string(body), 7))
			var envelope materialEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
			assert.Equal(t, tc.success, envelope.Success, recorder.Body.String())
			if tc.success {
				assert.Equal(t, &own.Data.Id, envelope.Data.GroupId)
				assert.Equal(t, "Portraits", envelope.Data.GroupName)
			}
		})
	}

	recorder = httptest.NewRecorder()
	c := newMaterialContext(recorder, http.MethodPut, "/api/material/groups/1", `{"name":"Characters"}`, 7)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(own.Data.Id)}}
	RenameMaterialGroup(c)
	var renamed materialGroupEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &renamed))
	require.True(t, renamed.Success)
	assert.Equal(t, "Characters", renamed.Data.Name)

	recorder = httptest.NewRecorder()
	c = newMaterialContext(recorder, http.MethodPut, "/api/material/groups/1", `{"name":"Stolen"}`, 8)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(own.Data.Id)}}
	RenameMaterialGroup(c)
	var denied materialGroupEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &denied))
	assert.False(t, denied.Success)

	recorder = httptest.NewRecorder()
	ListMaterials(newMaterialContext(recorder, http.MethodGet, "/api/material/list?group_id="+strconv.Itoa(own.Data.Id), "", 7))
	var list materialListEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &list))
	require.True(t, list.Success)
	require.Len(t, list.Data, 1)
	assert.Equal(t, "Characters", list.Data[0].GroupName)

	materialID := strconv.Itoa(list.Data[0].Id)
	for _, tc := range []struct {
		name, body string
		userID     int
		success    bool
	}{
		{"foreign material", `{"group_id":null}`, 8, false},
		{"foreign group", fmt.Sprintf(`{"group_id":%d}`, foreign.Data.Id), 7, false},
		{"missing field", `{}`, 7, false},
		{"ungroup", `{"group_id":null}`, 7, true},
		{"regroup", fmt.Sprintf(`{"group_id":%d}`, own.Data.Id), 7, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c := newMaterialContext(recorder, http.MethodPut, "/api/material/"+materialID+"/group", tc.body, tc.userID)
			c.Params = gin.Params{{Key: "id", Value: materialID}}
			UpdateMaterialGroup(c)
			var envelope materialEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
			assert.Equal(t, tc.success, envelope.Success, recorder.Body.String())
			if tc.success && tc.name == "ungroup" {
				assert.Nil(t, envelope.Data.GroupId)
				assert.Empty(t, envelope.Data.GroupName)
			}
		})
	}
}

type materialGroupEnvelope struct {
	Success bool                `json:"success"`
	Data    model.MaterialGroup `json:"data"`
}

type materialBeforeGroups struct {
	Id          int    `json:"id"`
	MaterialID  string `json:"material_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId      int    `json:"user_id" gorm:"index"`
	Name        string `json:"name" gorm:"type:varchar(255)"`
	LibraryType string `json:"library_type" gorm:"type:varchar(32);index"`
	FileType    string `json:"file_type" gorm:"type:varchar(32)"`
	SourceURL   string `json:"source_url" gorm:"type:text"`
	Status      string `json:"status" gorm:"type:varchar(32);index"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

func (materialBeforeGroups) TableName() string { return "materials" }

func TestMaterialGroupMigrationPreservesExistingMaterials(t *testing.T) {
	db := setupMaterialTestDB(t, &materialBeforeGroups{})
	var version string
	require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
	t.Logf("database: sqlite %s", version)
	legacy := materialBeforeGroups{MaterialID: "mat_legacy", UserId: 7, Name: "Existing image", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending_auth", CreatedTime: 1, UpdatedTime: 1}
	require.NoError(t, db.Create(&legacy).Error)
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.Material{}, &model.MaterialGroup{}))
		materials, total, err := model.SearchMaterials(model.MaterialFilter{UserId: 7})
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, materials, 1)
		assert.Equal(t, "Existing image", materials[0].Name)
		assert.Equal(t, publicMaterialURL, materials[0].SourceURL)
		assert.EqualValues(t, 1, materials[0].CreatedTime)
		assert.Nil(t, materials[0].GroupId)
		assert.Empty(t, materials[0].GroupName)
	}
	group := model.MaterialGroup{UserId: 7, Name: "Characters"}
	require.NoError(t, model.SaveMaterialGroup(&group))
	grouped, err := model.SetUserMaterialGroup(7, legacy.Id, &group.Id)
	require.NoError(t, err)
	assert.Equal(t, "Characters", grouped.GroupName)
	zero := 0
	ungrouped, total, err := model.SearchMaterials(model.MaterialFilter{UserId: 7, GroupId: &zero})
	require.NoError(t, err)
	assert.Empty(t, ungrouped)
	assert.Zero(t, total)
	_, err = model.SetUserMaterialGroup(7, legacy.Id, nil)
	require.NoError(t, err)
	ungrouped, total, err = model.SearchMaterials(model.MaterialFilter{UserId: 7, GroupId: &zero})
	require.NoError(t, err)
	assert.Len(t, ungrouped, 1)
	assert.EqualValues(t, 1, total)
	assert.NoError(t, db.Create(&model.Material{MaterialID: "mat_unique", UserId: 7}).Error)
	assert.Error(t, db.Create(&model.Material{MaterialID: "mat_unique", UserId: 7}).Error)
	duplicate := model.MaterialGroup{UserId: 7, Name: "Characters"}
	require.NoError(t, duplicate.Validate())
	assert.Error(t, db.Create(&duplicate).Error)
}

func TestDeleteMaterialRemovesOwnRecord(t *testing.T) {
	setupMaterialTestDB(t)
	now := common.GetTimestamp()
	require.NoError(t, model.DB.Create(&model.Material{
		Id: 5, MaterialID: "mat_own", UserId: 1, Name: "own", LibraryType: model.MaterialLibraryAIGC,
		FileType: model.MaterialFileImage, Status: model.MaterialStatusPending, CreatedTime: now, UpdatedTime: now,
	}).Error)

	recorder := httptest.NewRecorder()
	c := newMaterialContext(recorder, http.MethodDelete, "/api/material/5", "", 1)
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	DeleteMaterial(c)
	var envelope materialBoolEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.True(t, envelope.Success, recorder.Body.String())

	var count int64
	require.NoError(t, model.DB.Model(&model.Material{}).Count(&count).Error)
	assert.EqualValues(t, 0, count)
}

// setupAdminMaterialPostgres never initializes the application database. It uses
// only the pre-existing test database and rolls every fixture/request write back.
func setupAdminMaterialPostgres(t *testing.T) (*gorm.DB, []model.User) {
	t.Helper()
	if os.Getenv("MATERIAL_TEST_POSTGRES") != "1" {
		t.Skip("set MATERIAL_TEST_POSTGRES=1 to use the existing new-api-test PostgreSQL database")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	config, err := godotenv.Read(filepath.Join(filepath.Dir(filename), "..", ".env"))
	require.True(t, err == nil, "cannot read test database configuration")
	dsn := config["SQL_DSN"]
	parsed, err := url.Parse(dsn)
	require.True(t, err == nil && parsed != nil, "SQL_DSN must be a PostgreSQL URL")
	require.True(t, parsed.Scheme == "postgres" || parsed.Scheme == "postgresql", "SQL_DSN must be a PostgreSQL URL")
	require.Equal(t, "/new-api-test", parsed.Path, "refusing a database other than new-api-test")
	pgConfig, err := pgx.ParseConfig(dsn)
	require.True(t, err == nil, "invalid PostgreSQL configuration")
	require.Equal(t, "new-api-test", pgConfig.Database)
	for _, fallback := range pgConfig.Fallbacks {
		require.Equal(t, pgConfig.Host, fallback.Host, "refusing alternate database hosts")
	}
	pgConfig.ConnectTimeout = 10 * time.Second
	sqlDB := stdlib.OpenDB(*pgConfig)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var database, version string
	err = sqlDB.QueryRowContext(ctx, "SELECT current_database(), current_setting('server_version')").Scan(&database, &version)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			t.Logf("PostgreSQL connection SQLSTATE: %s", pgErr.Code)
		}
	}
	require.True(t, err == nil, "cannot verify PostgreSQL test database")
	require.Equal(t, "new-api-test", database, "refusing writes to a database other than new-api-test")
	t.Logf("database: PostgreSQL %s; database name verified as new-api-test", version)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = tx, tx
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.SetDatabaseTypes(previousMainType, previousLogType)
		require.NoError(t, tx.Rollback().Error)
	})
	// Recheck on the exact transaction connection before the first write.
	require.NoError(t, tx.Raw("SELECT current_database()").Scan(&database).Error)
	require.Equal(t, "new-api-test", database)
	// Serialize transactional migrations across targeted test processes.
	require.NoError(t, tx.Exec("SELECT pg_advisory_xact_lock(74329186)").Error)
	require.NoError(t, model.MigrateMaterialPreparation(tx))
	require.NoError(t, tx.AutoMigrate(&model.Task{}))
	users := make([]model.User, 3)
	for i := range users {
		key := common.GetUUID()
		role := common.RoleCommonUser
		if i == 0 {
			role = common.RoleAdminUser
		}
		users[i] = model.User{Username: "mat" + key[:16], AffCode: key, AccessToken: &key, Role: role, Status: common.UserStatusEnabled, AuthVersion: 1}
		require.NoError(t, tx.Create(&users[i]).Error)
	}
	return tx, users
}

func setupMaterialConcurrentPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("MATERIAL_TEST_POSTGRES") != "1" {
		t.Skip("set MATERIAL_TEST_POSTGRES=1 to use new-api-test")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	config, err := godotenv.Read(filepath.Join(filepath.Dir(filename), "..", ".env"))
	require.NoError(t, err)
	pgConfig, err := pgx.ParseConfig(config["SQL_DSN"])
	require.True(t, err == nil, "invalid PostgreSQL test configuration")
	require.Equal(t, "new-api-test", pgConfig.Database)
	pgConfig.ConnectTimeout = 10 * time.Second
	admin := stdlib.OpenDB(*pgConfig)
	t.Cleanup(func() { assert.NoError(t, admin.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var database, version string
	err = admin.QueryRowContext(ctx, "SELECT current_database(), current_setting('server_version')").Scan(&database, &version)
	require.True(t, err == nil, "cannot verify PostgreSQL test database")
	require.Equal(t, "new-api-test", database)
	t.Logf("database: PostgreSQL %s; database name verified as new-api-test", version)
	schema := "material_concurrent_" + strings.ReplaceAll(common.GetUUID(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE")
		assert.NoError(t, err)
	})
	pgConfig.RuntimeParams["search_path"] = schema
	pgConfig.RuntimeParams["statement_timeout"] = "15000"
	pgConfig.RuntimeParams["lock_timeout"] = "10000"
	sqlDB := stdlib.OpenDB(*pgConfig, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
		var database, currentSchema string
		if err := conn.QueryRow(ctx, "SELECT current_database(), current_schema()").Scan(&database, &currentSchema); err != nil {
			return errors.New("cannot verify isolated material test connection")
		}
		if database != "new-api-test" || currentSchema != schema {
			return errors.New("refusing material test writes outside the isolated test schema")
		}
		return nil
	}))
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	previousDB, previousRedis := model.DB, common.RedisEnabled
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, common.RedisEnabled = db, false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, previousLog)
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = previousDB, previousRedis
		common.SetDatabaseTypes(previousMain, previousLog)
	})
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, model.MigrateMaterialPreparation(db))
	return db
}

func TestVideoMaterialConcurrentReservations(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	user := model.User{Username: "concurrent-owner", AffCode: common.GetUUID()}
	require.NoError(t, db.Create(&user).Error)
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: user.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"}
	require.NoError(t, db.Create(&material).Error)
	binding := model.MaterialBinding{MaterialId: material.Id, Scope: "concurrent-scope", Version: model.MaterialSourceVersion(&material), AssetID: "shared-asset", GroupID: "shared-group", State: "ready", UpstreamStatus: "ACTIVE"}
	require.NoError(t, db.Create(&binding).Error)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	start, connected := make(chan struct{}), make(chan int, 2)
	results := make(chan error, 2)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
	}()
	for _, owner := range []string{"video-a", "video-b"} {
		workers.Go(func() {
			reported := false
			err := db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
				reported = true
				var pid int
				if err := conn.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
					connected <- 0
					return err
				}
				connected <- pid
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-start:
				}
				_, err := model.ReserveVideoMaterials(conn, user.Id, binding.Scope, owner, []string{binding.AssetID, binding.AssetID})
				return err
			})
			if !reported {
				connected <- 0
			}
			results <- err
		})
	}
	var pids []int
	for range 2 {
		select {
		case pid := <-connected:
			pids = append(pids, pid)
		case <-ctx.Done():
			t.Fatal("reservation connections did not become ready")
		}
	}
	close(start)
	firstErr, secondErr := <-results, <-results
	require.NotZero(t, pids[0])
	require.NotEqual(t, pids[0], pids[1], "reservations must use independent physical connections")
	assert.NoError(t, firstErr)
	assert.NoError(t, secondErr, "ready materials must support concurrent video submissions")
	var leases []model.MaterialUseLease
	require.NoError(t, db.Order("owner").Find(&leases).Error)
	require.Len(t, leases, 2, "duplicate references reserve once per submitting owner")
	assert.Equal(t, leases[0].BindingId, leases[1].BindingId)
	_, err := model.QueueMaterialDeletion(user.Id, material.Id)
	require.Error(t, err)
	require.NoError(t, model.ReleaseVideoMaterials(db, "video-a"))
	_, err = model.QueueMaterialDeletion(user.Id, material.Id)
	require.Error(t, err, "one video's release must not remove another video's protection")
	require.NoError(t, model.ReleaseVideoMaterials(db, "video-a"))
	var count int64
	require.NoError(t, db.Model(&model.MaterialUseLease{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&model.MaterialUseLease{}).Where("owner = ?", "video-b").Update("expires_at", common.GetTimestamp()-1).Error)
	_, err = model.ReserveVideoMaterials(db, user.Id, binding.Scope, "video-c", []string{binding.AssetID})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.MaterialUseLease{}).Count(&count).Error)
	assert.EqualValues(t, 1, count, "expired leases are cleaned without removing active owners")
	t.Run("deletion workers respect an existing shared lease", func(t *testing.T) {
		require.NoError(t, db.Model(&material).Update("deleting", true).Error)
		claimed, err := model.ClaimMaterialDeletionBinding(db, &binding, "delete-worker")
		require.NoError(t, err)
		assert.False(t, claimed, "an active video must prevent upstream deletion")
		require.NoError(t, db.Model(&binding).Update("state", "deleted").Error)
		require.NoError(t, model.FinalizeMaterialDeletion(db, &material))
		var preserved int64
		require.NoError(t, db.Model(&model.Material{}).Where("id = ?", material.Id).Count(&preserved).Error)
		assert.EqualValues(t, 1, preserved, "finalization must not erase an active video's material")
		require.NoError(t, db.Model(&material).Update("deleting", false).Error)
		require.NoError(t, db.Model(&binding).Update("state", "ready").Error)
	})
	require.NoError(t, model.ReleaseVideoMaterials(db, "video-c"))
	_, err = model.QueueMaterialDeletion(user.Id, material.Id)
	require.NoError(t, err)
	_, err = model.ReserveVideoMaterials(db, user.Id, binding.Scope, "too-late", []string{binding.AssetID})
	assert.Error(t, err, "queued deletion must block new reservations")
}

func TestVideoMaterialReservationRenewal(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	user := model.User{Username: "renewal-owner", AffCode: common.GetUUID()}
	require.NoError(t, db.Create(&user).Error)
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: user.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"}
	require.NoError(t, db.Create(&material).Error)
	binding := model.MaterialBinding{MaterialId: material.Id, Scope: "renewal", Version: model.MaterialSourceVersion(&material), AssetID: "renewed-asset", GroupID: "renewed-group", State: "ready", UpstreamStatus: "ACTIVE"}
	require.NoError(t, db.Create(&binding).Error)
	_, err := model.ReserveVideoMaterials(db, user.Id, binding.Scope, "same-submission", []string{binding.AssetID})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.MaterialUseLease{}).Where("owner = ?", "same-submission").Update("expires_at", common.GetTimestamp()+10).Error)
	deadline, err := model.ReserveVideoMaterials(db, user.Id, binding.Scope, "same-submission", []string{binding.AssetID})
	require.NoError(t, err)
	var leases []model.MaterialUseLease
	require.NoError(t, db.Find(&leases).Error)
	require.Len(t, leases, 1)
	assert.GreaterOrEqual(t, leases[0].ExpiresAt, deadline.Unix()+30, "a repeated reservation must protect its full submission deadline")
}

func TestVideoMaterialReadyLeaseHandoff(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	user := model.User{Username: "handoff-owner", AffCode: common.GetUUID()}
	require.NoError(t, db.Create(&user).Error)
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: user.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "processing"}
	require.NoError(t, db.Create(&material).Error)
	binding, claimed, err := model.ClaimMaterialBinding(db, &material, "handoff", "uploader")
	require.NoError(t, err)
	require.True(t, claimed)
	binding.State, binding.AssetID, binding.GroupID, binding.UpstreamStatus = "ready", "handoff-asset", "handoff-group", "ACTIVE"
	require.NoError(t, model.SaveMaterialBinding(db, binding, "uploader"))
	_, err = model.ReserveVideoMaterials(db, user.Id, binding.Scope, "waiting-video", []string{binding.AssetID})
	require.NoError(t, err, "ready must release upload exclusivity in the same database update")
	binding.State = "processing"
	assert.Error(t, model.SaveMaterialBinding(db, binding, "uploader"), "the uploader must not modify a binding after releasing ownership")
	stored := model.MaterialBinding{}
	require.NoError(t, db.First(&stored, binding.Id).Error)
	assert.Equal(t, "ready", stored.State)
	assert.Empty(t, stored.LockedBy)
	assert.Zero(t, stored.LockedUntil)
}

func TestVideoMaterialConcurrentDeletion(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	user := model.User{Username: "deletion-owner", AffCode: common.GetUUID()}
	require.NoError(t, db.Create(&user).Error)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	var connections []*gorm.DB
	var pids []int
	for range 2 {
		conn, err := sqlDB.Conn(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, conn.Close()) })
		session := db.Session(&gorm.Session{NewDB: true, Context: ctx})
		session.Statement.ConnPool = conn
		var pid int
		require.NoError(t, session.Raw("SELECT pg_backend_pid()").Scan(&pid).Error)
		connections, pids = append(connections, session), append(pids, pid)
	}
	require.NotEqual(t, pids[0], pids[1])
	previousDB := model.DB
	model.DB = connections[1]
	defer func() { model.DB = previousDB }()
	for _, winner := range []string{"reservation", "deletion"} {
		t.Run(winner+" holds the material lock first", func(t *testing.T) {
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: user.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?" + winner, Status: "ready"}
			require.NoError(t, db.Create(&material).Error)
			binding := model.MaterialBinding{MaterialId: material.Id, Scope: "delete-race", Version: model.MaterialSourceVersion(&material), AssetID: "asset-" + winner, GroupID: "race-group", State: "ready", UpstreamStatus: "ACTIVE"}
			require.NoError(t, db.Create(&binding).Error)
			held, release, attempting := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var workers sync.WaitGroup
			defer func() {
				unblock()
				workers.Wait()
			}()
			first, second := make(chan error, 1), make(chan error, 1)
			workers.Go(func() {
				first <- connections[0].Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec("SELECT id FROM materials WHERE id = ? FOR UPDATE", material.Id).Error; err != nil {
						close(held)
						return err
					}
					if winner == "reservation" {
						if _, err := model.ReserveVideoMaterials(tx, user.Id, binding.Scope, winner, []string{binding.AssetID}); err != nil {
							close(held)
							return err
						}
					} else if err := tx.Model(&material).Update("deleting", true).Error; err != nil {
						close(held)
						return err
					}
					close(held)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			})
			<-held
			workers.Go(func() {
				close(attempting)
				if winner == "reservation" {
					_, err := model.QueueMaterialDeletion(user.Id, material.Id)
					second <- err
				} else {
					_, err := model.ReserveVideoMaterials(connections[1], user.Id, binding.Scope, "late-video", []string{binding.AssetID})
					second <- err
				}
			})
			<-attempting
			unblock()
			require.NoError(t, <-first)
			require.Error(t, <-second, "deletion and a new reservation must never both succeed")
			require.NoError(t, db.First(&material, material.Id).Error)
			var count int64
			require.NoError(t, db.Model(&model.MaterialUseLease{}).Where("material_id = ?", material.Id).Count(&count).Error)
			if winner == "reservation" {
				assert.False(t, material.Deleting)
				assert.EqualValues(t, 1, count)
			} else {
				assert.True(t, material.Deleting)
				assert.Zero(t, count)
			}
		})
	}
}

func TestMaterialConcurrentDailyGroupCreation(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	user := model.User{Username: "group-owner", AffCode: common.GetUUID()}
	require.NoError(t, db.Create(&user).Error)
	materials := []model.Material{
		{MaterialID: model.NewPlatformMaterialID(), UserId: user.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?first", Status: "pending"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: user.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?second", Status: "pending"},
	}
	require.NoError(t, db.Create(&materials).Error)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	creating, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	defer func() {
		unblock()
		cancel()
		workers.Wait()
	}()
	first := &materialPreparationProvider{status: "ACTIVE", onGroupCreate: func() {
		close(creating)
		select {
		case <-ctx.Done():
		case <-release:
		}
	}}
	second := &materialPreparationProvider{status: "ACTIVE"}
	result := make(chan error, 1)
	workers.Go(func() { result <- service.PrepareMaterial(ctx, &materials[0], "shared-daily", first) })
	select {
	case <-creating:
	case <-ctx.Done():
		t.Fatal("group creator did not reach the upstream boundary")
	}
	require.NoError(t, service.PrepareMaterial(ctx, &materials[1], "shared-daily", second))
	var waiting model.MaterialBinding
	require.NoError(t, db.Where("material_id = ? AND scope = ?", materials[1].Id, "shared-daily").First(&waiting).Error)
	assert.Equal(t, "pending", waiting.State, "an active creator is not an unknown group result")
	assert.Empty(t, waiting.FailReason)
	assert.Zero(t, second.groups)
	assert.Zero(t, second.groupLists, "do not query ownership while another creator is active")
	waitingForGroup := make(chan struct{}, 1)
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("material_test_waiting_daily_group", func(tx *gorm.DB) {
		group, ok := tx.Statement.Dest.(*model.MaterialUpstreamGroup)
		if ok && group.Scope == "shared-daily" && group.State == "creating" && group.LockedBy != "" {
			select {
			case waitingForGroup <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(func() { assert.NoError(t, db.Callback().Query().Remove("material_test_waiting_daily_group")) })
	type videoResult struct {
		resolved []string
		issues   []service.MaterialPreparationIssue
		err      error
	}
	video := make(chan videoResult, 1)
	workers.Go(func() {
		resolved, issues, err := service.PrepareVideoMaterials(ctx, user.Id, service.MaterialChannel{Scope: "shared-daily", Client: second}, []service.MaterialImageInput{{Source: materials[1].SourceURL}})
		video <- videoResult{resolved, issues, err}
	})
	select {
	case <-waitingForGroup:
	case <-ctx.Done():
		t.Fatal("video request did not observe the active group creator")
	}
	unblock()
	require.NoError(t, <-result)
	prepared := <-video
	require.NoError(t, prepared.err)
	require.Empty(t, prepared.issues, "the same video request must wait for group creation instead of requiring a retry")
	assert.Equal(t, []string{"asset://upstream-asset"}, prepared.resolved)
	require.NoError(t, db.First(&waiting, waiting.Id).Error)
	assert.Equal(t, "ready", waiting.State)
	assert.Equal(t, "upstream-group", waiting.GroupID)
	assert.Equal(t, 1, first.groups)
	assert.Zero(t, second.groups)
	assert.Equal(t, 1, second.assets)
}

func TestMaterialConcurrentSourceUpload(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	user := model.User{Username: "upload-owner", AffCode: common.GetUUID()}
	require.NoError(t, db.Create(&user).Error)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	var lookupWorkers sync.WaitGroup
	defer func() {
		cancel()
		lookupWorkers.Wait()
	}()
	connected, start := make(chan int, 2), make(chan struct{})
	type lookupResult struct {
		material *model.Material
		err      error
	}
	found := make(chan lookupResult, 2)
	for range 2 {
		lookupWorkers.Go(func() {
			var material *model.Material
			reported := false
			err := db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
				reported = true
				var pid int
				if err := conn.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
					connected <- 0
					return err
				}
				connected <- pid
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-start:
				}
				var err error
				material, err = model.FindOrCreateVideoMaterial(conn, user.Id, publicMaterialURL, "single-upload")
				return err
			})
			if !reported {
				connected <- 0
			}
			found <- lookupResult{material, err}
		})
	}
	var pids []int
	for range 2 {
		select {
		case pid := <-connected:
			pids = append(pids, pid)
		case <-ctx.Done():
			t.Fatal("source lookup connections did not become ready")
		}
	}
	close(start)
	first, second := <-found, <-found
	require.NotZero(t, pids[0])
	require.NotEqual(t, pids[0], pids[1])
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, first.material.Id, second.material.Id)
	var count int64
	require.NoError(t, db.Model(&model.Material{}).Where("user_id = ?", user.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	creating, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	defer func() {
		unblock()
		cancel()
		workers.Wait()
	}()
	provider := &materialPreparationProvider{status: "ACTIVE", onAssetCreate: func() {
		close(creating)
		select {
		case <-ctx.Done():
		case <-release:
		}
	}}
	result := make(chan error, 1)
	workers.Go(func() { result <- service.PrepareMaterial(ctx, first.material, "single-upload", provider) })
	select {
	case <-creating:
	case <-ctx.Done():
		t.Fatal("uploader did not reach the upstream boundary")
	}
	other := &materialPreparationProvider{status: "ACTIVE"}
	require.NoError(t, service.PrepareMaterial(ctx, second.material, "single-upload", other))
	assert.Zero(t, other.groups)
	assert.Zero(t, other.assets)
	unblock()
	require.NoError(t, <-result)
	require.NoError(t, service.PrepareMaterial(ctx, second.material, "single-upload", other))
	assert.Equal(t, 1, provider.groups)
	assert.Equal(t, 1, provider.assets)
	assert.Zero(t, other.assets, "concurrent and subsequent requests must reuse the upload")
}

func TestMaterialGroupCreationLeaseTakeover(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	group := model.MaterialUpstreamGroup{UserId: 1, Scope: "lease-scope", Name: "group-20261105", State: "pending"}
	require.NoError(t, db.Create(&group).Error)
	claimed, create, err := model.ClaimMaterialUpstreamGroup(db, &group, "old-owner")
	require.NoError(t, err)
	require.True(t, claimed)
	require.True(t, create)
	require.NoError(t, db.Model(&group).Update("locked_until", common.GetTimestamp()-1).Error)
	current := model.MaterialUpstreamGroup{Id: group.Id}
	claimed, create, err = model.ClaimMaterialUpstreamGroup(db, &current, "new-owner")
	require.NoError(t, err)
	require.True(t, claimed)
	assert.False(t, create, "an expired creator must only query, never create again")
	group.State, group.UpstreamID = "ready", "stale-result"
	assert.Error(t, model.SaveMaterialUpstreamGroup(db, &group, "old-owner"))
	require.NoError(t, db.Model(&group).Where("locked_by = ?", "old-owner").Updates(map[string]any{"locked_by": "", "locked_until": 0}).Error)
	current.State, current.UpstreamID = "ready", "confirmed-result"
	require.NoError(t, model.SaveMaterialUpstreamGroup(db, &current, "new-owner"))
	require.NoError(t, db.First(&current, current.Id).Error)
	assert.Equal(t, "confirmed-result", current.UpstreamID)
	assert.Equal(t, "new-owner", current.LockedBy)
}

func TestMaterialLeaseMigration(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	group := model.MaterialUpstreamGroup{UserId: 1, Scope: "migration", Name: "group-20261105", UpstreamID: "existing-group", State: "ready"}
	require.NoError(t, db.Create(&group).Error)
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: 1, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"}
	require.NoError(t, db.Create(&material).Error)
	binding := model.MaterialBinding{MaterialId: material.Id, Scope: group.Scope, Version: model.MaterialSourceVersion(&material), AssetID: "existing-asset", GroupID: group.UpstreamID, State: "ready", UpstreamStatus: "ACTIVE", LockedBy: "legacy-submit", LockedUntil: common.GetTimestamp() + 120}
	require.NoError(t, db.Create(&binding).Error)
	require.NoError(t, db.Migrator().DropTable(&model.MaterialUseLease{}))
	require.NoError(t, db.Migrator().DropColumn(&model.MaterialUpstreamGroup{}, "LockedBy"))
	require.NoError(t, db.Migrator().DropColumn(&model.MaterialUpstreamGroup{}, "LockedUntil"))
	for range 2 {
		require.NoError(t, model.MigrateMaterialPreparation(db))
	}
	var stored model.MaterialBinding
	require.NoError(t, db.First(&stored, binding.Id).Error)
	assert.Equal(t, binding, stored, "upgrade must preserve IDs, readiness and existing exclusive leases")
	require.NoError(t, db.First(&group, group.Id).Error)
	assert.Equal(t, "existing-group", group.UpstreamID)
	assert.Equal(t, "ready", group.State)
	lease := model.MaterialUseLease{MaterialId: material.Id, BindingId: binding.Id, Owner: "migration-submit", ExpiresAt: common.GetTimestamp() + 120}
	require.NoError(t, db.Create(&lease).Error)
	duplicate := lease
	duplicate.Id = 0
	assert.Error(t, db.Create(&duplicate).Error, "each owner reserves a binding only once")
	require.NoError(t, model.MigrateMaterialPreparation(db))
	var count int64
	require.NoError(t, db.Model(&model.MaterialUseLease{}).Count(&count).Error)
	assert.EqualValues(t, 1, count, "repeated migration preserves live reservations")
}

func TestAdminMaterialUserFilter(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Material{}))
	keyword := common.GetUUID()
	materials := []model.Material{
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: keyword + " first", LibraryType: "AIGC", FileType: "Image", Status: "pending_auth"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: keyword + " second", LibraryType: "AIGC", FileType: "Video", Status: "ready"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[2].Id, Name: keyword + " foreign", LibraryType: "AIGC", FileType: "Image", Status: "pending_auth"},
	}
	require.NoError(t, db.Create(&materials).Error)
	require.NoError(t, db.Delete(&users[2]).Error)
	usernames := map[int]string{
		materials[0].Id: users[1].Username,
		materials[1].Id: users[1].Username,
		materials[2].Id: users[2].Username,
	}
	for _, tc := range []struct {
		name, query string
		total       int64
		ids         []int
	}{
		{"missing means all", "", 3, []int{materials[2].Id, materials[1].Id, materials[0].Id}},
		{"owner filter", "&user_id=" + strconv.Itoa(users[1].Id), 2, []int{materials[1].Id, materials[0].Id}},
		{"existing owner without materials", "&user_id=" + strconv.Itoa(users[0].Id), 0, []int{}},
		{"count before paging", fmt.Sprintf("&user_id=%d&offset=1&limit=1", users[1].Id), 2, []int{materials[0].Id}},
		{"empty page retains count", fmt.Sprintf("&user_id=%d&offset=2&limit=1", users[1].Id), 2, []int{}},
		{"combined filters", fmt.Sprintf("&user_id=%d&status=ready&library_type=AIGC&group_id=0", users[1].Id), 1, []int{materials[1].Id}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c := newMaterialContext(recorder, http.MethodGet, "/api/material/all?keyword="+keyword+tc.query, "", users[0].Id)
			ListAllMaterials(c)
			var envelope materialListEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
			require.True(t, envelope.Success)
			assert.Equal(t, tc.total, envelope.Total)
			ids := make([]int, 0, len(envelope.Data))
			for _, material := range envelope.Data {
				ids = append(ids, material.Id)
				assert.Equal(t, usernames[material.Id], material.Username, "material %d owner username", material.Id)
			}
			assert.Equal(t, tc.ids, ids)
			assert.Equal(t, users[0].Id, c.GetInt("id"), "owner filtering must not replace the actor")
		})
	}
	t.Run("self list ignores foreign owner", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		target := fmt.Sprintf("/api/material/list?keyword=%s&user_id=%d", keyword, users[2].Id)
		c := newMaterialContext(recorder, http.MethodGet, target, "", users[1].Id)
		ListMaterials(c)
		var envelope materialListEnvelope
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		assert.EqualValues(t, 2, envelope.Total)
		require.Len(t, envelope.Data, 2)
		assert.Equal(t, []int{materials[1].Id, materials[0].Id}, []int{envelope.Data[0].Id, envelope.Data[1].Id})
		for _, material := range envelope.Data {
			assert.Equal(t, users[1].Id, material.UserId)
			assert.Equal(t, users[1].Username, material.Username)
		}
		assert.Equal(t, users[1].Id, c.GetInt("id"))
	})
	var missing int
	require.NoError(t, db.Unscoped().Model(&model.User{}).Select("COALESCE(MAX(id), 0) + 1").Scan(&missing).Error)
	t.Run("missing owner omits username", func(t *testing.T) {
		orphan := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: missing, Name: keyword + " orphan", LibraryType: "AIGC", FileType: "Image", Status: "pending_auth"}
		require.NoError(t, db.Create(&orphan).Error)
		recorder := httptest.NewRecorder()
		ListAllMaterials(newMaterialContext(recorder, http.MethodGet, "/api/material/all?keyword="+url.QueryEscape(orphan.Name), "", users[0].Id))
		var envelope materialListEnvelope
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		assert.EqualValues(t, 1, envelope.Total)
		require.Len(t, envelope.Data, 1)
		assert.Equal(t, orphan, envelope.Data[0])
		assert.NotContains(t, recorder.Body.String(), `"username"`)
	})
	for _, owner := range []string{"", "0", "-1", "abc", "1.5", "999999999999999999999999", strconv.Itoa(missing)} {
		t.Run("invalid owner "+owner, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ListAllMaterials(newMaterialContext(recorder, http.MethodGet, "/api/material/all?user_id="+owner, "", users[0].Id))
			var envelope materialListEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
			assert.False(t, envelope.Success, "invalid owners must not fall back to all users")
			assert.Len(t, envelope.Data, 0)
		})
	}
	for _, query := range []string{"user_id=%zz", "user_id=1;user_id=2", fmt.Sprintf("user_id=%d&user_id=%d", users[1].Id, users[2].Id)} {
		t.Run("malformed filter "+query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ListAllMaterials(newMaterialContext(recorder, http.MethodGet, "/api/material/all?keyword="+keyword+"&"+query, "", users[0].Id))
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			var envelope materialListEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
			assert.False(t, envelope.Success)
			assert.Len(t, envelope.Data, 0)
		})
	}
}

func TestAdminMaterialUpstreamGroupNames(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	key := common.GetUUID()
	localGroup := model.MaterialGroup{UserId: users[1].Id, Name: "Managed portraits"}
	require.NoError(t, model.SaveMaterialGroup(&localGroup))
	materials := []model.Material{
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: key + " automatic", LibraryType: "AIGC", FileType: "Image", Status: "ready"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: key + " multiple channels", LibraryType: "AIGC", FileType: "Image", Status: "ready"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: key + " foreign owner", LibraryType: "AIGC", FileType: "Image", Status: "ready"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: key + " different scope", LibraryType: "AIGC", FileType: "Image", Status: "ready"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: key + " different group ID", LibraryType: "AIGC", FileType: "Image", Status: "ready"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: key + " stale source", LibraryType: "AIGC", FileType: "Image", Status: "pending"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: key + " ungrouped", LibraryType: "AIGC", FileType: "Image", Status: "pending"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: key + " managed", LibraryType: "AIGC", FileType: "Image", Status: "pending", GroupId: &localGroup.Id},
	}
	require.NoError(t, db.Create(&materials).Error)
	groups := []model.MaterialUpstreamGroup{
		{UserId: users[1].Id, Scope: key + "-one", Name: "aigc-first", UpstreamID: "shared-group", State: "ready"},
		{UserId: users[1].Id, Scope: key + "-two", Name: "aigc-second", UpstreamID: "shared-group", State: "ready"},
		{UserId: users[1].Id, Scope: key + "-duplicate", Name: "aigc-first", UpstreamID: "duplicate-group", State: "ready"},
		{UserId: users[2].Id, Scope: key + "-one", Name: "foreign-name", UpstreamID: "shared-group", State: "ready"},
		{UserId: users[2].Id, Scope: key + "-foreign", Name: "foreign-only", UpstreamID: "shared-group", State: "ready"},
	}
	require.NoError(t, db.Create(&groups).Error)
	bindings := []model.MaterialBinding{
		{MaterialId: materials[0].Id, Scope: key + "-one", GroupID: "shared-group", Version: model.MaterialSourceVersion(&materials[0])},
		{MaterialId: materials[1].Id, Scope: key + "-one", GroupID: "shared-group", Version: model.MaterialSourceVersion(&materials[1])},
		{MaterialId: materials[1].Id, Scope: key + "-two", GroupID: "shared-group", Version: model.MaterialSourceVersion(&materials[1])},
		{MaterialId: materials[1].Id, Scope: key + "-duplicate", GroupID: "duplicate-group", Version: model.MaterialSourceVersion(&materials[1])},
		{MaterialId: materials[2].Id, Scope: key + "-foreign", GroupID: "shared-group", Version: model.MaterialSourceVersion(&materials[2])},
		{MaterialId: materials[3].Id, Scope: key + "-unknown", GroupID: "shared-group", Version: model.MaterialSourceVersion(&materials[3])},
		{MaterialId: materials[4].Id, Scope: key + "-one", GroupID: "different-group", Version: model.MaterialSourceVersion(&materials[4])},
		{MaterialId: materials[5].Id, Scope: key + "-one", GroupID: "shared-group", Version: "previous-source"},
	}
	require.NoError(t, db.Create(&bindings).Error)

	recorder := httptest.NewRecorder()
	ListAllMaterials(newMaterialContext(recorder, http.MethodGet, "/api/material/all?keyword="+key, "", users[0].Id))
	var list materialListEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &list))
	require.True(t, list.Success, recorder.Body.String())
	require.Len(t, list.Data, len(materials))
	byID := make(map[int]model.Material, len(list.Data))
	for _, material := range list.Data {
		byID[material.Id] = material
	}
	for i, want := range []string{"aigc-first", "aigc-first, aigc-second", "", "", "", "", "", "Managed portraits"} {
		t.Run(strings.TrimPrefix(materials[i].Name, key+" "), func(t *testing.T) {
			assert.Equal(t, want, byID[materials[i].Id].GroupName, "list group name")
			assert.Equal(t, materials[i].GroupId, byID[materials[i].Id].GroupId, "display must not change local membership")
			recorder := httptest.NewRecorder()
			c := newMaterialContext(recorder, http.MethodGet, "/api/material/admin/users/owner/materials/material", "", users[0].Id)
			c.Params = gin.Params{{Key: "user_id", Value: strconv.Itoa(users[1].Id)}, {Key: "id", Value: strconv.Itoa(materials[i].Id)}}
			AdminGetMaterial(c)
			var detail materialEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &detail))
			require.True(t, detail.Success, recorder.Body.String())
			assert.Equal(t, want, detail.Data.GroupName, "detail group name")
		})
	}
}

func TestMobileCloudMaterialScopeCanonicalEndpoint(t *testing.T) {
	config := service.MobileCloudMaterialConfiguration{Endpoint: service.MobileCloudMaterialDefaultEndpoint, AccessKey: "account", PoolID: "pool"}
	want := service.MobileCloudMaterialScope(&config, 5)
	for _, endpoint := range []string{"https://ecloud.10086.cn", "https://ecloud.10086.cn/", "https://ecloud.10086.cn/api/openapi-maas/", "https://ECLOUD.10086.CN:443/api/openapi-maas"} {
		config.Endpoint = endpoint
		assert.Equal(t, want, service.MobileCloudMaterialScope(&config, 5), endpoint)
	}
	config.Endpoint = service.MobileCloudMaterialDefaultEndpoint
	config.PoolID = "other-pool"
	assert.NotEqual(t, want, service.MobileCloudMaterialScope(&config, 5))
	channel := model.Channel{Key: "key", BaseURL: common.GetPointer("https://video.example")}
	fingerprint := service.MobileCloudVideoChannelFingerprint(&channel)
	channel.HeaderOverride = common.GetPointer("null")
	assert.Equal(t, fingerprint, service.MobileCloudVideoChannelFingerprint(&channel), "absent header overrides have one identity")
}

func TestAIGCMaterialDailyGroupMigration(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("fresh=%t", fresh), func(t *testing.T) {
			tx := db
			require.NoError(t, tx.SavePoint("daily_group_schema").Error)
			t.Cleanup(func() { require.NoError(t, tx.RollbackTo("daily_group_schema").Error) })
			if fresh {
				require.NoError(t, tx.Migrator().DropTable(&model.MaterialUpstreamGroup{}))
			} else {
				legacy := model.MaterialUpstreamGroup{UserId: users[1].Id, Scope: "migration-scope", Name: "aigc-legacy", UpstreamID: "legacy-group", State: "ready"}
				require.NoError(t, tx.Create(&legacy).Error)
				require.NoError(t, tx.Migrator().DropColumn(&model.MaterialUpstreamGroup{}, "CreationToken"))
				require.NoError(t, tx.Migrator().DropColumn(&model.MaterialBinding{}, "GroupName"))
				if tx.Migrator().HasIndex(&model.MaterialUpstreamGroup{}, "idx_material_group_scope_name") {
					require.NoError(t, tx.Migrator().DropIndex(&model.MaterialUpstreamGroup{}, "idx_material_group_scope_name"))
				}
				require.NoError(t, tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_material_group_scope ON material_upstream_groups (user_id, scope)").Error)
			}
			for range 2 {
				require.NoError(t, model.MigrateMaterialPreparation(tx))
			}
			groups := []model.MaterialUpstreamGroup{
				{UserId: users[1].Id, Scope: "migration-scope", Name: "group-20261104", UpstreamID: "first-day", State: "ready"},
				{UserId: users[1].Id, Scope: "migration-scope", Name: "group-20261105", UpstreamID: "second-day", State: "ready"},
			}
			require.NoError(t, tx.Create(&groups).Error, "different dates must coexist within the same channel")
			require.NoError(t, tx.SavePoint("duplicate_daily_group").Error)
			group := groups[0]
			group.Id = 0
			assert.Error(t, tx.Create(&group).Error, "same date remains unique within its user and channel")
			require.NoError(t, tx.RollbackTo("duplicate_daily_group").Error)
			if !fresh {
				var legacy model.MaterialUpstreamGroup
				require.NoError(t, tx.Where("user_id = ? AND scope = ? AND name = ?", users[1].Id, "migration-scope", "aigc-legacy").First(&legacy).Error)
				assert.Equal(t, "legacy-group", legacy.UpstreamID)
				assert.Equal(t, "ready", legacy.State)
			}
		})
	}
}

func TestAIGCMaterialDailyGroups(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	beforeMidnight := time.Date(2026, time.November, 4, 15, 59, 59, 0, time.UTC)
	afterMidnight := beforeMidnight.Add(time.Second)
	key := common.GetUUID()
	providers := map[string]*materialPreparationProvider{}
	var materials []model.Material
	for _, tc := range []struct {
		name, scope, groupName, groupID string
		at                              time.Time
		owner                           int
	}{
		{"first image", key + "-mobile", "group-20261104", "mobile-day-one", beforeMidnight, users[1].Id},
		{"same day second image", key + "-mobile", "group-20261104", "mobile-day-one", beforeMidnight, users[1].Id},
		{"next day image", key + "-mobile", "group-20261105", "mobile-day-two", afterMidnight, users[1].Id},
		{"other channel same image", key + "-topenrouter", "group-20261104", "topenrouter-day-one", beforeMidnight, users[1].Id},
		{"other user same channel", key + "-mobile", "group-20261104", "other-user-day-one", beforeMidnight, users[2].Id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := publicMaterialURL
			if tc.name == "same day second image" || tc.name == "next day image" {
				source += "?image=" + url.QueryEscape(tc.name)
			}
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: tc.owner, Name: key + " " + tc.name, LibraryType: "AIGC", FileType: "Image", SourceURL: source, Status: "pending", CreatedTime: tc.at.Unix()}
			require.NoError(t, db.Create(&material).Error)
			binding := model.MaterialBinding{MaterialId: material.Id, Scope: tc.scope, Version: model.MaterialSourceVersion(&material), State: "pending", Name: "aigc-" + material.MaterialID, StartedAt: tc.at.Unix()}
			require.NoError(t, db.Create(&binding).Error)
			provider := providers[tc.groupID]
			if provider == nil {
				provider = &materialPreparationProvider{status: "ACTIVE", groupID: tc.groupID}
				providers[tc.groupID] = provider
			}
			require.NoError(t, service.PrepareMaterial(t.Context(), &material, tc.scope, provider))
			require.NoError(t, db.First(&binding, binding.Id).Error)
			assert.Equal(t, "ready", binding.State)
			assert.Equal(t, tc.groupID, binding.GroupID)
			assert.Equal(t, tc.groupName, provider.groupName)
			assert.Equal(t, 1, provider.groups, "one group per user, resource space and day")
			assert.False(t, provider.groupDescriptionProvided, "group creation must omit the description parameter")
			materials = append(materials, material)
		})
	}
	var groups []model.MaterialUpstreamGroup
	require.NoError(t, db.Where("user_id IN ?", []int{users[1].Id, users[2].Id}).Find(&groups).Error)
	assert.Len(t, groups, 4)
	for _, group := range groups {
		assert.Contains(t, []string{"group-20261104", "group-20261105"}, group.Name)
	}
	require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", materials[0].Id).Update("started_at", afterMidnight.Unix()).Error)
	require.NoError(t, service.PrepareMaterial(t.Context(), &materials[0], key+"-mobile", providers["mobile-day-one"]))
	assert.Equal(t, 1, providers["mobile-day-one"].groups)
	assert.Equal(t, 2, providers["mobile-day-one"].assets, "reuse ready images without moving or uploading them on the next day")

	recorder := httptest.NewRecorder()
	ListAllMaterials(newMaterialContext(recorder, http.MethodGet, "/api/material/all?keyword="+key, "", users[0].Id))
	var list materialListEnvelope
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &list))
	require.True(t, list.Success, recorder.Body.String())
	require.Len(t, list.Data, len(materials))
	for _, material := range list.Data {
		want := "group-20261104"
		if strings.Contains(material.Name, "next day") {
			want = "group-20261105"
		}
		assert.Equal(t, want, material.GroupName, material.Name)
	}
}

func TestAIGCMaterialDailyGroupUnknownRecovery(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	at := time.Date(2026, time.November, 4, 15, 59, 59, 0, time.UTC)
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "uncertain daily group", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&material).Error)
	scope := common.GetUUID()
	binding := model.MaterialBinding{MaterialId: material.Id, Scope: scope, Version: model.MaterialSourceVersion(&material), State: "pending", Name: "aigc-" + material.MaterialID, StartedAt: at.Unix()}
	require.NoError(t, db.Create(&binding).Error)
	provider := &materialPreparationProvider{status: "ACTIVE", groupErr: context.DeadlineExceeded}
	require.NoError(t, service.PrepareMaterial(t.Context(), &material, scope, provider))
	assert.Equal(t, "group-20261104", provider.groupName)
	assert.False(t, provider.groupDescriptionProvided)
	assert.Zero(t, provider.assets)
	provider.groupErr = nil
	for _, description := range []string{"someone-else", ""} {
		provider.groupPages = [][]service.MobileCloudAssetGroup{{
			{ID: "unconfirmed-group", Type: "AIGC", Name: provider.groupName, Description: description},
		}}
		retried, err := model.RetryUserMaterial(users[1].Id, material.Id)
		require.NoError(t, err)
		require.NoError(t, service.PrepareMaterial(t.Context(), retried, scope, provider))
		require.NoError(t, db.First(&binding, binding.Id).Error)
		assert.Equal(t, "uncertain", binding.State)
		assert.Empty(t, binding.GroupID, "a same-name group cannot prove ownership without a description marker")
		assert.Equal(t, "group-20261104", binding.GroupName)
		assert.Contains(t, binding.FailReason, "unknown")
		assert.Equal(t, 1, provider.groups, "never create a new day group after an unknown result")
		assert.Zero(t, provider.assets)
	}
}

func TestAIGCMaterialLegacyGroupRecovery(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, state := range []string{"pending", "uncertain"} {
		t.Run(state, func(t *testing.T) {
			scope := common.GetUUID()
			group := model.MaterialUpstreamGroup{UserId: users[1].Id, Scope: scope, Name: "aigc-" + common.GetUUID(), State: "creating"}
			require.NoError(t, db.Create(&group).Error)
			require.NoError(t, db.Model(&group).Update("creation_token", nil).Error)
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "legacy group " + state, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
			require.NoError(t, db.Create(&material).Error)
			binding := model.MaterialBinding{MaterialId: material.Id, Scope: scope, Version: model.MaterialSourceVersion(&material), State: state, Name: "aigc-" + material.MaterialID, StartedAt: common.GetTimestamp()}
			require.NoError(t, db.Create(&binding).Error)
			provider := &materialPreparationProvider{status: "ACTIVE", groupName: group.Name}
			require.NoError(t, service.PrepareMaterial(t.Context(), &material, scope, provider))
			require.NoError(t, db.First(&binding, binding.Id).Error)
			assert.Equal(t, "ready", binding.State)
			assert.Equal(t, "upstream-group", binding.GroupID)
			assert.Equal(t, group.Name, binding.GroupName)
			assert.Zero(t, provider.groups, "recover the unknown legacy creation instead of creating a daily group")
			assert.Equal(t, 1, provider.assets)
			var groups []model.MaterialUpstreamGroup
			require.NoError(t, db.Where("user_id = ? AND scope = ?", users[1].Id, scope).Find(&groups).Error)
			require.Len(t, groups, 1)
			assert.Equal(t, group.Name, groups[0].Name)
			assert.Equal(t, "upstream-group", groups[0].UpstreamID)
			assert.Empty(t, groups[0].CreationToken, "legacy NULL markers must not inherit a new group's random token")
		})
	}
}

func TestAIGCMaterialPreparationLifecycle(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Material{}, &model.MaterialUpstreamGroup{}, &model.MaterialBinding{}))
	require.NoError(t, db.AutoMigrate(&model.Material{}, &model.MaterialUpstreamGroup{}, &model.MaterialBinding{}))
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "portrait", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&material).Error)
	provider := &materialPreparationProvider{status: "PROCESSING"}
	ctx := context.Background()
	require.NoError(t, service.PrepareMaterial(ctx, &material, "scope-one", provider))
	require.NoError(t, db.First(&material, material.Id).Error)
	assert.Equal(t, "processing", material.Status, "an AssetId does not mean ready")
	assert.Equal(t, 1, provider.groups)
	assert.Equal(t, 1, provider.assets)
	require.NoError(t, service.PrepareMaterial(ctx, &material, "scope-one", provider))
	assert.Equal(t, 1, provider.assets, "polling must reuse the uploaded asset")
	provider.status = "ACTIVE"
	require.NoError(t, service.PrepareMaterial(ctx, &material, "scope-one", provider))
	require.NoError(t, db.First(&material, material.Id).Error)
	assert.Equal(t, "ready", material.Status)
	require.NoError(t, service.PrepareMaterial(ctx, &material, "scope-one", provider))
	assert.Equal(t, 1, provider.assets)
	require.NoError(t, service.PrepareMaterial(ctx, &material, "scope-two", provider))
	assert.Equal(t, 2, provider.groups, "resource spaces must not share group IDs")
	assert.Equal(t, 2, provider.assets, "resource spaces must not share asset IDs")
	body, err := common.Marshal(material)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "upstream-asset")
	assert.NotContains(t, string(body), "upstream-group")
}

func TestMobileCloudVideoPluginContract(t *testing.T) {
	source, err := builtinplugins.Source("mobile-cloud")
	require.NoError(t, err)
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{Key: "mobile-cloud"})
	require.NoError(t, err)
	body := map[string]any{"model": "seedance-2.0", "content": []any{map[string]any{"type": "text", "text": "video"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}, "role": "last_frame"}}, "duration": 5, "watermark": false, "seed": 0}
	value, err := plugin.Engine.CallPath(t.Context(), "native", []string{"createTask"}, map[string]any{"body": map[string]any{"kind": "json", "value": body}})
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var intent map[string]any
	require.NoError(t, common.Unmarshal(encoded, &intent))
	value, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"baseUrl": "https://upstream.example/v1", "apiKey": "test-key", "model": "seedance-2.0", "upstreamModel": "seedance-2.5", "requestBody": intent["requestBody"]})
	require.NoError(t, err)
	encoded, err = common.Marshal(value)
	require.NoError(t, err)
	var descriptor struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    map[string]any    `json:"body"`
	}
	require.NoError(t, common.Unmarshal(encoded, &descriptor))
	assert.Equal(t, "https://upstream.example/v1/contents/generations/tasks", descriptor.URL)
	assert.Equal(t, "Bearer test-key", descriptor.Headers["Authorization"])
	assert.Equal(t, "seedance-2.5", descriptor.Body["model"])
	assert.Equal(t, false, descriptor.Body["watermark"])
	assert.Equal(t, float64(0), descriptor.Body["seed"])
	content := descriptor.Body["content"].([]any)
	assert.Equal(t, "text", content[0].(map[string]any)["type"])
	assert.Equal(t, "last_frame", content[1].(map[string]any)["role"])
	for _, status := range []string{"queued", "running", "succeeded", "failed", "unexpected"} {
		value, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, map[string]any{"status": status, "content": map[string]any{"video_url": "https://example.com/video.mp4"}})
		require.NoError(t, err)
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		var result map[string]any
		require.NoError(t, common.Unmarshal(encoded, &result))
		assert.Equal(t, map[string]string{"queued": "QUEUED", "running": "IN_PROGRESS", "succeeded": "SUCCESS", "failed": "FAILURE", "unexpected": "UNKNOWN"}[status], result["status"])
	}
	for _, duration := range []any{0, -1, 3601, 2.5} {
		_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"baseUrl": "https://upstream.example", "model": "seedance-2.0", "requestBody": map[string]any{"prompt": "video", "duration": duration}})
		require.Error(t, err)
	}
}

func TestMobileCloudVideoRejectsChangedDestination(t *testing.T) {
	db, _ := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	channel := model.Channel{Name: "bound video account", Key: "account-a", BaseURL: common.GetPointer("https://video.example"), Status: 1}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	config := service.MobileCloudMaterialConfiguration{ChannelID: channel.Id, Endpoint: service.MobileCloudMaterialDefaultEndpoint, AccessKey: "test-access", SecretKey: "test-secret", PoolID: "test-pool"}
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(config))
	require.NoError(t, db.Model(&channel).Update("key", "account-b").Error)
	body := map[string]any{"content": []any{map[string]any{"type": "text", "text": "video"}}}
	_, _, _, err := service.PrepareMobileCloudVideoBody(t.Context(), 1, &channel, body)
	require.Error(t, err, "changing video account must invalidate the material resource-space association")
	loaded, loadErr := service.LoadMaterialChannels()
	require.Error(t, loadErr, "background preparation must reject the same changed account")
	assert.Empty(t, loaded)
	otherChannel := channel
	otherChannel.Id++
	prepared, issues, reservation, err := service.PrepareMobileCloudVideoBody(t.Context(), 1, &otherChannel, body)
	require.NoError(t, err)
	assert.Equal(t, body, prepared)
	assert.Empty(t, issues)
	assert.Nil(t, reservation)
	var stored model.Option
	require.NoError(t, db.Where("key = ?", model.MobileCloudMaterialConfigOption).First(&stored).Error)
	t.Run("legacy encrypted configuration", func(t *testing.T) {
		key, err := base64.StdEncoding.DecodeString(os.Getenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY"))
		require.NoError(t, err)
		block, err := aes.NewCipher(key)
		require.NoError(t, err)
		aead, err := cipher.NewGCM(block)
		require.NoError(t, err)
		legacyConfig := config
		legacyConfig.ChannelFingerprint = service.MobileCloudVideoChannelFingerprint(&channel)
		plain, err := common.Marshal(legacyConfig)
		require.NoError(t, err)
		nonce := make([]byte, aead.NonceSize())
		sealed := aead.Seal(nonce, nonce, plain, []byte(model.MobileCloudMaterialConfigOption+":v1"))
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", "v1."+base64.StdEncoding.EncodeToString(sealed)).Error)
		t.Cleanup(func() {
			require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", stored.Value).Error)
		})
		loaded, err := service.LoadMobileCloudMaterialConfiguration()
		require.NoError(t, err)
		assert.Equal(t, &legacyConfig, loaded)
		prepared, _, _, err := service.PrepareMobileCloudVideoBody(t.Context(), 1, &otherChannel, body)
		require.NoError(t, err)
		assert.Equal(t, body, prepared)
		t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "invalid")
		_, _, _, err = service.PrepareMobileCloudVideoBody(t.Context(), 1, &otherChannel, body)
		assert.Error(t, err, "legacy configurations with unknown bindings must fail closed")
	})
	t.Run("encrypted binding rejects a changed channel header", func(t *testing.T) {
		parts := strings.SplitN(stored.Value, ".", 3)
		require.Len(t, parts, 3)
		tampered := fmt.Sprintf("v2.%d.%s", otherChannel.Id, parts[2])
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", tampered).Error)
		t.Cleanup(func() {
			require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", stored.Value).Error)
		})
		_, err := service.LoadMobileCloudMaterialConfiguration()
		assert.Error(t, err)
		_, _, _, err = service.PrepareMobileCloudVideoBody(t.Context(), 1, &otherChannel, body)
		assert.Error(t, err)
	})
	for _, failure := range []string{"missing key", "corrupted ciphertext"} {
		t.Run(failure, func(t *testing.T) {
			if failure == "missing key" {
				t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "")
				t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY_FILE", filepath.Join(t.TempDir(), "missing.key"))
			} else {
				require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", stored.Value[:len(stored.Value)-8]+"AAAAAAAA").Error)
				t.Cleanup(func() {
					require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", stored.Value).Error)
				})
			}
			prepared, issues, reservation, err := service.PrepareMobileCloudVideoBody(t.Context(), 1, &otherChannel, body)
			require.NoError(t, err, "credential failures must not block an unrelated video channel")
			assert.Equal(t, body, prepared)
			assert.Empty(t, issues)
			assert.Nil(t, reservation)
			_, _, _, err = service.PrepareMobileCloudVideoBody(t.Context(), 1, &channel, body)
			assert.Error(t, err, "the bound channel must never bypass the material gate")
		})
	}
	t.Run("unknown channel binding blocks submission", func(t *testing.T) {
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", "corrupted-encrypted-config").Error)
		t.Cleanup(func() {
			require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", stored.Value).Error)
		})
		_, _, _, err := service.PrepareMobileCloudVideoBody(t.Context(), 1, &otherChannel, body)
		assert.Error(t, err, "an unknown binding must not be treated as an unrelated channel")
	})
	require.NoError(t, db.Where("key = ?", model.MobileCloudMaterialConfigOption).Delete(&model.Option{}).Error)
	prepared, issues, reservation, err = service.PrepareMobileCloudVideoBody(t.Context(), 1, &channel, body)
	require.NoError(t, err)
	assert.Equal(t, body, prepared)
	assert.Empty(t, issues)
	assert.Nil(t, reservation)
}

func TestMobileCloudVideoSubmitMaterialGate(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	channel := model.Channel{Name: "Video material channel", Key: "unused-test-key", Status: 1}
	require.NoError(t, db.Create(&channel).Error)
	config := service.MobileCloudMaterialConfiguration{ChannelID: channel.Id, Endpoint: service.MobileCloudMaterialDefaultEndpoint, AccessKey: "test-access", SecretKey: "test-secret", PoolID: "test-pool"}
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(config))
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "existing video image", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"}
	require.NoError(t, db.Create(&material).Error)
	binding := model.MaterialBinding{MaterialId: material.Id, Scope: service.MobileCloudMaterialScope(&config, channel.Id), Version: model.MaterialSourceVersion(&material), ChannelID: channel.Id, AssetID: "existing-asset", GroupID: "existing-group", State: "ready", UpstreamStatus: "ACTIVE"}
	require.NoError(t, db.Create(&binding).Error)
	source, err := builtinplugins.Source("mobile-cloud")
	require.NoError(t, err)
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{Key: "mobile-cloud"})
	require.NoError(t, err)
	service.InitHttpClient()
	modelName := "seedance-2.0"
	previousPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"seedance-2.0":0.01}`))
	var submitted []map[string]any
	upstreamStatus := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := common.DecodeJson(r.Body, &body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		submitted = append(submitted, body)
		content := body["content"].([]any)
		if content[0].(map[string]any)["image_url"].(map[string]any)["url"] == "asset://existing-asset" {
			_, deletionErr := model.QueueMaterialDeletion(users[1].Id, material.Id)
			assert.Error(t, deletionErr, "video submission must reserve its material until the request returns")
		}
		if upstreamStatus == 0 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if assert.NoError(t, err) {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamStatus)
		_, _ = w.Write([]byte(`{"id":"video-job"}`))
	}))
	defer server.Close()
	require.NoError(t, db.Model(&channel).Update("base_url", server.URL).Error)
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(config))
	for _, tc := range []struct {
		name      string
		channelID int
		input     string
		want      string
		blocked   bool
		status    int
		disabled  bool
	}{
		{"reuse library URL", channel.Id, publicMaterialURL, "asset://existing-asset", false, http.StatusOK, false},
		{"reuse asset reference", channel.Id, "asset://existing-asset", "asset://existing-asset", false, http.StatusOK, false},
		{"disabled integration keeps original URL", channel.Id, publicMaterialURL, publicMaterialURL, false, http.StatusOK, true},
		{"re-enabled integration restores material substitution", channel.Id, publicMaterialURL, "asset://existing-asset", false, http.StatusOK, false},
		{"upstream rejection releases material", channel.Id, publicMaterialURL, "", false, http.StatusBadRequest, false},
		{"accepted request without response is not replayed", channel.Id, publicMaterialURL, "", false, 0, false},
		{"unconfigured channel preserves original URL", channel.Id + 1, publicMaterialURL, publicMaterialURL, false, http.StatusOK, false},
		{"foreign asset rejected", channel.Id, "asset://foreign-asset", "", true, http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() {
				var active int64
				require.NoError(t, db.Model(&model.MaterialUseLease{}).Where("material_id = ?", material.Id).Count(&active).Error)
				assert.Zero(t, active, "successful, rejected and interrupted submissions must release shared reservations")
			})
			if tc.disabled {
				config.Enabled = common.GetPointer(false)
				require.NoError(t, service.SaveMobileCloudMaterialConfiguration(config))
				queries := 0
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("material_toggle_no_queries", func(tx *gorm.DB) {
					if tx.Statement.Table == "materials" || tx.Statement.Table == "material_bindings" || tx.Statement.Table == "material_groups" {
						queries++
					}
				}))
				t.Cleanup(func() {
					assert.Zero(t, queries, "disabled video integration must not query, upload, wait for or reserve materials")
					require.NoError(t, db.Callback().Query().Remove("material_toggle_no_queries"))
					config.Enabled = common.GetPointer(true)
					require.NoError(t, service.SaveMobileCloudMaterialConfiguration(config))
				})
			}
			upstreamStatus = tc.status
			request := map[string]any{"model": modelName, "prompt": "video", "images": []string{tc.input}, "metadata": map[string]any{"resolution": "720p"}}
			original, err := common.Marshal(request)
			require.NoError(t, err)
			c := newMaterialContext(httptest.NewRecorder(), http.MethodPost, "/v1/videos", string(original), users[1].Id)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
			common.SetContextKey(c, constant.ContextKeyChannelId, tc.channelID)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, channel.Key)
			c.Set("task_request", request)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			events := []string{}
			info := &relaycommon.RelayInfo{UserId: users[1].Id, OriginModelName: modelName, UserGroup: "default", UsingGroup: "default", Billing: &taskSubmissionTestBilling{events: &events}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			before := len(submitted)
			result, taskErr := relay.RelayTaskSubmit(c, info)
			if tc.blocked {
				require.NotNil(t, taskErr)
				assert.Equal(t, "material_preparation_failed", taskErr.Code)
				assert.True(t, taskErr.NoRetry)
				assert.Equal(t, before, len(submitted), "no video request before every image is ready")
				return
			}
			if tc.status == http.StatusOK {
				require.Nil(t, taskErr, "%+v", taskErr)
				require.NotNil(t, result)
				value, recorded := c.Get("task_event_upstream_request")
				if tc.channelID == channel.Id && !tc.disabled {
					require.True(t, recorded)
					upstream, ok := value.(json.RawMessage)
					require.True(t, ok)
					want, err := common.Marshal(submitted[before])
					require.NoError(t, err)
					assert.JSONEq(t, string(want), string(upstream))
				} else {
					assert.False(t, recorded)
				}
			} else if tc.status == 0 {
				require.NotNil(t, taskErr)
				assert.Equal(t, "do_request_failed", taskErr.Code)
				assert.Equal(t, "视频提交结果暂无法确认，请勿重复提交", taskErr.Message)
				assert.True(t, taskErr.NoRetry, "a request accepted without a response must never be automatically replayed")
			} else {
				require.NotNil(t, taskErr)
				assert.Equal(t, "fail_to_fetch_task", taskErr.Code)
				assert.Equal(t, "视频请求处理失败，请联系管理员", taskErr.Message)
			}
			require.Len(t, submitted, before+1)
			content := submitted[before]["content"].([]any)
			if tc.status == http.StatusOK {
				assert.Equal(t, tc.want, content[0].(map[string]any)["image_url"].(map[string]any)["url"])
			}
			assert.Equal(t, []string{tc.input}, request["images"], "the original request stays unchanged for retry and logging")
			if tc.disabled {
				return
			}
			var stored model.MaterialBinding
			require.NoError(t, db.First(&stored, binding.Id).Error)
			assert.Empty(t, stored.LockedBy)
			assert.Zero(t, stored.LockedUntil, "submission must release the reservation")
		})
	}
	t.Run("task events retain original and prepared model requests", func(t *testing.T) {
		upstreamStatus = http.StatusOK
		oldConsume := common.LogConsumeEnabled
		common.LogConsumeEnabled = false
		t.Cleanup(func() { common.LogConsumeEnabled = oldConsume })
		request := map[string]any{"model": modelName, "prompt": "video", "images": []string{publicMaterialURL}, "metadata": map[string]any{"resolution": "720p"}}
		original, err := common.Marshal(request)
		require.NoError(t, err)
		c := newMaterialContext(httptest.NewRecorder(), http.MethodPost, "/v1/videos", string(original), users[1].Id)
		common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
		common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
		common.SetContextKey(c, constant.ContextKeyChannelKey, channel.Key)
		c.Set("task_request", request)
		c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
		billingEvents := []string{}
		info := &relaycommon.RelayInfo{UserId: users[1].Id, UserQuota: common.MaxQuota, OriginModelName: modelName, UserGroup: "default", UsingGroup: "default", Billing: &taskSubmissionTestBilling{events: &billingEvents}, TaskRelayInfo: &relaycommon.TaskRelayInfo{LockedChannel: &channel}}
		before := len(submitted)
		outcome, taskErr := executeTaskSubmissionWith(c, info, relay.RelayTaskSubmit)
		require.Nil(t, taskErr, "%+v", taskErr)
		require.NotNil(t, outcome)
		require.Len(t, submitted, before+1)
		events, err := model.GetTaskEvents(outcome.Task.ID)
		require.NoError(t, err)
		require.Len(t, events, 2)
		raw := events[0]
		require.NoError(t, events[0].ProjectDetail(true))
		assert.JSONEq(t, string(original), events[0].Payload)
		want, err := common.Marshal(submitted[before])
		require.NoError(t, err)
		assert.JSONEq(t, string(want), events[0].UpstreamRequest)
		assert.Contains(t, events[0].UpstreamRequest, "asset://existing-asset")
		require.NoError(t, raw.ProjectDetail(false))
		assert.Empty(t, raw.UpstreamRequest)
		assert.NotContains(t, raw.Payload, "asset://existing-asset")
	})
	t.Run("shared reservations release independently after cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		body := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}}}}
		_, issues, first, err := service.PrepareMobileCloudVideoBody(ctx, users[1].Id, &channel, body)
		require.NoError(t, err)
		require.Empty(t, issues)
		require.NotNil(t, first)
		defer first.Release()
		_, issues, second, err := service.PrepareMobileCloudVideoBody(t.Context(), users[1].Id, &channel, body)
		require.NoError(t, err)
		require.Empty(t, issues)
		require.NotNil(t, second)
		defer second.Release()
		var active int64
		require.NoError(t, db.Model(&model.MaterialUseLease{}).Where("binding_id = ?", binding.Id).Count(&active).Error)
		assert.EqualValues(t, 2, active)
		cancel()
		first.Release()
		first.Release()
		require.NoError(t, db.Model(&model.MaterialUseLease{}).Where("binding_id = ?", binding.Id).Count(&active).Error)
		assert.EqualValues(t, 1, active, "cancellation must not stop cleanup or remove the other submission's lease")
		_, err = model.QueueMaterialDeletion(users[1].Id, material.Id)
		assert.Error(t, err)
		second.Release()
		require.NoError(t, db.Model(&model.MaterialUseLease{}).Where("binding_id = ?", binding.Id).Count(&active).Error)
		assert.Zero(t, active)
	})
	t.Run("reservation conflict identifies the material", func(t *testing.T) {
		require.NoError(t, db.Model(&binding).Updates(map[string]any{"locked_by": "concurrent-video", "locked_until": common.GetTimestamp() + 120}).Error)
		t.Cleanup(func() {
			require.NoError(t, db.Model(&binding).Updates(map[string]any{"locked_by": "", "locked_until": 0}).Error)
		})
		body := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}, "role": "first_frame"}}}
		prepared, issues, release, err := service.PrepareMobileCloudVideoBody(t.Context(), users[1].Id, &channel, body)
		require.NoError(t, err)
		assert.Nil(t, prepared)
		assert.Nil(t, release)
		assert.Equal(t, []service.MaterialPreparationIssue{{Index: 0, MaterialID: material.MaterialID, Field: "content[0].image_url.url", Usage: "first_frame", Status: "unavailable", Reason: "Material is unavailable or in use; retry after the current operation finishes"}}, issues)
		assert.Equal(t, publicMaterialURL, body["content"].([]any)[0].(map[string]any)["image_url"].(map[string]any)["url"])
	})
	t.Run("submission deadline is bound to the stored reservation", func(t *testing.T) {
		body := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}}}}
		prepared, issues, reservation, err := service.PrepareMobileCloudVideoBody(t.Context(), users[1].Id, &channel, body)
		require.NoError(t, err)
		require.Empty(t, issues)
		require.NotNil(t, prepared)
		require.NotNil(t, reservation)
		defer reservation.Release()
		var stored model.MaterialUseLease
		require.NoError(t, db.Where("binding_id = ?", binding.Id).First(&stored).Error)
		assert.Equal(t, time.Unix(stored.ExpiresAt, 0).Add(-30*time.Second), reservation.Deadline)
	})
	t.Run("account changes during preparation block submission", func(t *testing.T) {
		changed := false
		require.NoError(t, db.Callback().Update().After("gorm:update").Register("material_test_rebind_during_preparation", func(tx *gorm.DB) {
			if tx.Statement.Table == "materials" && !changed {
				changed = true
				tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", "replacement-video-key").Error)
			}
		}))
		t.Cleanup(func() {
			require.NoError(t, db.Callback().Update().Remove("material_test_rebind_during_preparation"))
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", channel.Key).Error)
		})
		body := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}}}}
		prepared, issues, reservation, err := service.PrepareMobileCloudVideoBody(t.Context(), users[1].Id, &channel, body)
		require.Error(t, err)
		assert.True(t, changed)
		assert.Nil(t, prepared)
		assert.Empty(t, issues)
		assert.Nil(t, reservation)
		var stored model.MaterialBinding
		require.NoError(t, db.First(&stored, binding.Id).Error)
		assert.Empty(t, stored.LockedBy)
	})
	t.Run("one failed image blocks the complete batch", func(t *testing.T) {
		failed := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "failed last frame", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?last", Status: "failed"}
		require.NoError(t, db.Create(&failed).Error)
		failedBinding := model.MaterialBinding{MaterialId: failed.Id, Scope: binding.Scope, Version: model.MaterialSourceVersion(&failed), State: "failed", FailReason: "Unsupported image format", CanRetry: true}
		require.NoError(t, db.Create(&failedBinding).Error)
		request := map[string]any{"model": modelName, "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}, "role": "first_frame"}, map[string]any{"type": "text", "text": "video"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": failed.SourceURL}, "role": "last_frame"}}}
		c := newMaterialContext(httptest.NewRecorder(), http.MethodPost, "/v1/contents/generations/tasks", "", users[1].Id)
		common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
		common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
		common.SetContextKey(c, constant.ContextKeyChannelKey, channel.Key)
		c.Set("task_request", request)
		c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
		events := []string{}
		billing := &taskSubmissionTestBilling{events: &events}
		info := &relaycommon.RelayInfo{UserId: users[1].Id, OriginModelName: modelName, UserGroup: "default", UsingGroup: "default", Billing: billing, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		before := len(submitted)
		result, taskErr := executeTaskSubmissionWith(c, info, relay.RelayTaskSubmit)
		assert.Nil(t, result)
		require.NotNil(t, taskErr)
		assert.Equal(t, "material_preparation_failed", taskErr.Code)
		assert.True(t, taskErr.NoRetry)
		assert.Equal(t, before, len(submitted))
		assert.Equal(t, 1, billing.refunds)
		issues, ok := taskErr.Data.([]service.MaterialPreparationIssue)
		require.True(t, ok)
		assert.Equal(t, "参考图片（第 3 项）格式不支持，请更换图片", taskErr.Message)
		assert.Equal(t, []service.MaterialPreparationIssue{{Index: 2, Field: "content[2].image_url.url", Usage: "last_frame", Status: "failed", Reason: "参考图片格式不支持，请更换图片"}}, issues)
		assert.Equal(t, publicMaterialURL, request["content"].([]any)[0].(map[string]any)["image_url"].(map[string]any)["url"], "a partial batch must not rewrite the original request")
	})
}

func TestDeferredVideoSubmissionReportsMaterialFailure(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.TaskEvent{}, &model.Channel{}, &model.Token{}, &model.SystemTask{}, &model.Log{}, &model.Option{}))
	previousLogDB := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = previousLogDB })
	users := make([]model.User, 2)
	for i := range users {
		users[i] = model.User{Username: fmt.Sprintf("async-material-%d", i), AffCode: fmt.Sprintf("async-material-%d", i), Status: common.UserStatusEnabled}
		require.NoError(t, db.Create(&users[i]).Error)
	}
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	videoCalls, videoStatus := 0, http.StatusOK
	videoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		videoCalls++
		if videoStatus != http.StatusOK {
			w.WriteHeader(videoStatus)
			_, _ = w.Write([]byte(`{"error":"not configured"}`))
			return
		}
		var body map[string]any
		if assert.NoError(t, common.DecodeJson(r.Body, &body)) {
			content := body["content"].([]any)
			assert.Equal(t, "asset://async-asset", content[0].(map[string]any)["image_url"].(map[string]any)["url"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"async-upstream"}`))
	}))
	t.Cleanup(videoServer.Close)
	uploads := 0
	materialServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/openapi-maas/exp/aicc/v2")
		switch {
		case r.Method == http.MethodPost && path == "/asset-group":
			_, _ = w.Write([]byte(`{"state":"OK","body":{"groupId":"async-group","groupType":"AIGC"}}`))
		case r.Method == http.MethodPost && path == "/asset":
			uploads++
			_, _ = w.Write([]byte(`{"state":"OK","body":"async-asset"}`))
		case r.Method == http.MethodGet && path == "/asset/async-asset":
			_, _ = w.Write([]byte(`{"state":"OK","body":{"assetId":"async-asset","groupId":"async-group","assetType":"Image","status":"ACTIVE"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(materialServer.Close)
	previousTransport := http.DefaultTransport
	transport := previousTransport.(*http.Transport).Clone()
	transport.DialTLSContext = (&tls.Dialer{Config: &tls.Config{RootCAs: materialServer.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}}).DialContext
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	service.InitHttpClient()
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
	channel := model.Channel{Name: "async video mock", Type: constant.ChannelTypeTaskPlugin, Key: "test-key", BaseURL: &videoServer.URL, Status: 1, Setting: common.GetPointer(`{"task_plugin_key":"async-material"}`)}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{ChannelID: channel.Id, Endpoint: materialServer.URL, AccessKey: "test-access", SecretKey: "test-secret", PoolID: "test-pool"}))
	plugin, err := pluginruntime.CompilePlugin(`
		export const meta = {apiVersion:1,key:"async-material",name:"async-material",version:"1.0.0",fetchMode:"per_task",models:["async-video"],author:{name:"test"},usageSchema:{}};
		export function buildSubmitRequest(ctx) {return {url:ctx.baseUrl+"/tasks",method:"POST",body:ctx.requestBody};}
		export function parseSubmitResponse(ctx, response) {return {taskId:response.body.id,taskData:response.body};}
		export function buildQueryRequest(ctx) {return {url:ctx.baseUrl+"/tasks/"+ctx.taskId};}
		export function parseTaskResult() {return {status:"UNKNOWN"};}
	`, pluginruntime.Options{Key: "async-material"})
	require.NoError(t, err)
	prices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"async-video":0.01}`))
	c := newMaterialContext(httptest.NewRecorder(), http.MethodPost, "/tasks", `{}`, users[1].Id)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "async-video")
	common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
	common.SetContextKey(c, constant.ContextKeyChannelKey, channel.Key)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, videoServer.URL)
	c.Set("task_request", map[string]any{"model": "async-video", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://missing-image"}, "role": "reference_image"}}})
	c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
	require.NoError(t, db.Model(&users[1]).Update("quota", 100000000).Error)
	token := model.Token{UserId: users[1].Id, Key: common.GetUUID(), RemainQuota: 100000000}
	require.NoError(t, db.Create(&token).Error)
	info := &relaycommon.RelayInfo{UserId: users[1].Id, TokenId: token.Id, TokenKey: token.Key, UserQuota: 100000000, OriginModelName: "async-video", UserGroup: "default", UsingGroup: "default", TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionImageToVideo, LockedChannel: &channel}}
	info.UserSetting.BillingPreference = "wallet_only"
	outcome, taskErr := executeTaskSubmission(c, info)
	require.Nil(t, taskErr, "%+v", taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, "pending", outcome.Task.SubmissionStage)
	assert.Empty(t, outcome.Task.PrivateData.UpstreamTaskID)
	assert.NotEmpty(t, outcome.Task.TaskID)
	presentTaskSubmission(c, outcome)
	assert.Contains(t, c.Writer.Header().Get("Content-Type"), "application/json")
	for _, polled := range model.GetAllUnFinishSyncTasks(100) {
		assert.NotEqual(t, outcome.Task.ID, polled.ID)
	}

	err = runVideoSubmission(t.Context(), outcome.Task, plugin)
	require.NoError(t, err)
	var failed model.Task
	require.NoError(t, db.First(&failed, outcome.Task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, failed.Status)
	assert.Equal(t, "参考图片（第 1 项）不可用，请更换图片", failed.FailReason)
	view, err := service.BuildTaskPluginView(&failed)
	require.NoError(t, err)
	data, ok := view.Data.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, data, "error")
	publicVideo := failed.ToOpenAIVideo()
	require.NotNil(t, publicVideo.Error)
	assert.Equal(t, failed.FailReason, publicVideo.Error.Message)
	assert.Empty(t, failed.PrivateData.VideoSubmission.Request)
	assert.Zero(t, videoCalls, "unready material must never reach the video provider")
	assert.Zero(t, failed.Quota)
	assert.Equal(t, "failed_final", failed.SubmissionStage)
	var balanceAfterRefund int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", users[1].Id).Select("quota").Scan(&balanceAfterRefund).Error)
	require.NoError(t, runVideoSubmission(t.Context(), &failed, plugin))
	var balanceAfterRetry int
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", users[1].Id).Select("quota").Scan(&balanceAfterRetry).Error)
	assert.Equal(t, balanceAfterRefund, balanceAfterRetry)
	assert.Equal(t, 100000000, balanceAfterRefund)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 100000000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)

	originalRequest := `{"model":"async-video","prompt":"original client prompt","client_extension":"preserved"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(originalRequest))
	c.Request.Header.Set("Content-Type", "application/json")
	common.CleanupBodyStorage(c)
	c.Set("task_request", map[string]any{"model": "async-video", "prompt": "normalized provider prompt", "generate_audio": false, "api_key": "upstream-credential-canary", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}, "role": "reference_image"}}})
	info.TaskRelayInfo.PublicTaskID = ""
	info.Billing = nil
	accepted, taskErr := executeTaskSubmission(c, info)
	require.Nil(t, taskErr, "%+v", taskErr)
	assert.Zero(t, uploads, "HTTP acceptance must not upload material")
	assert.Zero(t, videoCalls, "HTTP acceptance must not submit the provider request")
	require.NoError(t, runVideoSubmission(t.Context(), accepted.Task, plugin))
	var submitted model.Task
	require.NoError(t, db.First(&submitted, accepted.Task.ID).Error)
	assert.EqualValues(t, model.TaskStatusSubmitted, submitted.Status)
	assert.Equal(t, "async-upstream", submitted.GetUpstreamTaskID())
	assert.Equal(t, accepted.Task.TaskID, submitted.TaskID)
	assert.Empty(t, submitted.SubmissionStage)
	assert.Equal(t, 1, uploads)
	assert.Equal(t, 1, videoCalls)
	events, err := model.GetTaskEvents(submitted.ID)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.NotContains(t, events[0].Payload, "upstream-credential-canary")
	assert.Equal(t, submitted.SubmitTime, events[0].Timestamp)
	for _, role := range []int{common.RoleAdminUser, common.RoleCommonUser} {
		recorder := httptest.NewRecorder()
		viewer, _ := gin.CreateTestContext(recorder)
		viewer.Request = httptest.NewRequest(http.MethodGet, "/api/task/events", nil)
		viewer.Params = gin.Params{{Key: "task_id", Value: strconv.FormatInt(submitted.ID, 10)}}
		viewer.Set("id", users[1].Id)
		viewer.Set("role", role)
		GetDashboardTaskEvents(viewer)
		require.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Data []model.TaskEvent `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.Len(t, response.Data, 2)
		event := response.Data[0]
		assert.NotContains(t, recorder.Body.String(), "upstream-credential-canary")
		if role >= common.RoleAdminUser {
			assert.JSONEq(t, originalRequest, event.Payload)
			require.NotEmpty(t, event.UpstreamRequest, "background submission must retain the actual model request")
			assert.Contains(t, event.UpstreamRequest, "asset://async-asset")
			assert.Contains(t, event.UpstreamRequest, "normalized provider prompt")
			assert.Contains(t, event.UpstreamRequest, `"generate_audio":false`)
			assert.Contains(t, event.UpstreamRequest, "[REDACTED]")
		} else {
			assert.Empty(t, event.UpstreamRequest)
			assert.NotContains(t, recorder.Body.String(), "normalized provider prompt")
			assert.NotContains(t, recorder.Body.String(), "asset://async-asset")
		}
	}
	t.Run("late client snapshot preserves the upstream request", func(t *testing.T) {
		require.NoError(t, model.RecordTaskEventDetail(submitted.ID, "request", submitted.SubmitTime, model.TaskEventRequestDetail{Request: json.RawMessage(originalRequest)}))
		stored, err := model.GetTaskEvents(submitted.ID)
		require.NoError(t, err)
		require.NoError(t, stored[0].ProjectDetail(true))
		assert.JSONEq(t, originalRequest, stored[0].Payload)
		assert.Contains(t, stored[0].UpstreamRequest, "asset://async-asset")
	})
	t.Run("oversized upstream request preserves the original request", func(t *testing.T) {
		upstream, err := common.Marshal(map[string]any{"prompt": strings.Repeat("u", model.TaskEventPayloadLimit)})
		require.NoError(t, err)
		require.NoError(t, model.RecordTaskEventDetail(submitted.ID, "request", submitted.SubmitTime, model.TaskEventRequestDetail{UpstreamRequest: upstream}))
		stored, err := model.GetTaskEvents(submitted.ID)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(stored[0].Payload), model.TaskEventPayloadLimit)
		require.NoError(t, stored[0].ProjectDetail(true))
		assert.JSONEq(t, originalRequest, stored[0].Payload)
		assert.JSONEq(t, `{"unavailable":"size_limit_exceeded"}`, stored[0].UpstreamRequest)
	})
	require.NoError(t, runVideoSubmission(t.Context(), &submitted, plugin))
	assert.Equal(t, 1, videoCalls, "a finished submission must not be replayed")
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 100000000-submitted.Quota, token.RemainQuota)
	assert.Equal(t, submitted.Quota, token.UsedQuota)
	var material model.Material
	require.NoError(t, db.Where("user_id = ? AND source_url = ?", users[1].Id, publicMaterialURL).First(&material).Error)
	require.NoError(t, db.Model(&material).Update("status", model.MaterialStatusFailed).Error)
	require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", material.Id).Updates(map[string]any{"state": "failed", "upstream_status": "FAILED", "fail_reason": "Image rejected by content review", "can_retry": false}).Error)
	c.Set("task_request", map[string]any{"model": "async-video", "content": []any{map[string]any{"type": "text", "text": "reference image"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://async-asset"}, "role": "reference_image"}}})
	info.TaskRelayInfo.PublicTaskID, info.Billing = "", nil
	rejected, taskErr := executeTaskSubmission(c, info)
	require.Nil(t, taskErr, "%+v", taskErr)
	require.NoError(t, runVideoSubmission(t.Context(), rejected.Task, plugin))
	var rejectedTask model.Task
	require.NoError(t, db.First(&rejectedTask, rejected.Task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, rejectedTask.Status)
	assert.Equal(t, "参考图片（第 2 项）未通过审核，请更换图片", rejectedTask.FailReason)
	assert.NotContains(t, string(rejectedTask.Data), material.MaterialID)
	assert.NotContains(t, string(rejectedTask.Data), "Image rejected by content review")
	assert.Contains(t, string(rejectedTask.Data), `"field":"content[1].image_url.url"`)
	assert.Contains(t, string(rejectedTask.Data), `"usage":"reference_image"`)
	assert.Equal(t, 1, videoCalls)
	assert.Equal(t, 1, uploads)
	t.Run("configuration rejection remains private", func(t *testing.T) {
		reason := "Material preparation rejected: topenrouter material HTTP status 403: not configured"
		require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", material.Id).Update("fail_reason", reason).Error)
		info.TaskRelayInfo.PublicTaskID, info.Billing = "", nil
		outcome, taskErr := executeTaskSubmission(c, info)
		require.Nil(t, taskErr, "%+v", taskErr)
		require.NoError(t, runVideoSubmission(t.Context(), outcome.Task, plugin))
		var failed model.Task
		require.NoError(t, db.First(&failed, outcome.Task.ID).Error)
		assert.Equal(t, "视频服务异常，请联系管理员", failed.FailReason)
		public, err := service.BuildTaskPluginView(&failed)
		require.NoError(t, err)
		encoded, err := common.Marshal(public)
		require.NoError(t, err)
		for _, private := range []string{"topenrouter", "403", "not configured", material.MaterialID, "not ready"} {
			assert.NotContains(t, string(encoded), private)
		}
		video := failed.ToOpenAIVideo()
		require.NotNil(t, video.Error)
		assert.Equal(t, failed.FailReason, video.Error.Message)
		var binding model.MaterialBinding
		require.NoError(t, db.Where("material_id = ?", material.Id).First(&binding).Error)
		assert.Equal(t, reason, binding.FailReason, "administrator diagnostics must retain the original failure")
		assert.Equal(t, 1, videoCalls)
		assert.Equal(t, 1, uploads)
		assert.Zero(t, failed.Quota)
	})
	t.Run("video authorization rejection keeps the service error", func(t *testing.T) {
		videoStatus = http.StatusForbidden
		t.Cleanup(func() { videoStatus = http.StatusOK })
		require.NoError(t, db.Model(&material).Update("status", model.MaterialStatusReady).Error)
		require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", material.Id).Updates(map[string]any{"state": "ready", "upstream_status": "ACTIVE", "fail_reason": ""}).Error)
		info.TaskRelayInfo.PublicTaskID, info.Billing = "", nil
		outcome, taskErr := executeTaskSubmission(c, info)
		require.Nil(t, taskErr, "%+v", taskErr)
		require.NoError(t, runVideoSubmission(t.Context(), outcome.Task, plugin))
		var failed model.Task
		require.NoError(t, db.First(&failed, outcome.Task.ID).Error)
		assert.Equal(t, "视频服务异常，请联系管理员", failed.FailReason)
		assert.Equal(t, "failed_final", failed.SubmissionStage)
		assert.NotContains(t, string(failed.Data), "not configured")
		assert.Equal(t, 2, videoCalls)
		assert.Equal(t, 1, uploads)
		assert.Zero(t, failed.Quota)
	})
}

func TestVideoSubmissionSettlementReloadsConfirmedBilling(t *testing.T) {
	db := setupMaterialTestDB(t, &model.Task{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.Log{})
	previousLogDB, previousRedis := model.LOG_DB, common.RedisEnabled
	model.LOG_DB, common.RedisEnabled = db, false
	t.Cleanup(func() { model.LOG_DB, common.RedisEnabled = previousLogDB, previousRedis })
	user := model.User{Username: "video-confirmation", Quota: 900, UsedQuota: 100}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "video-confirmation", RemainQuota: 900, UsedQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "video-confirmation", UsedQuota: 100}
	require.NoError(t, db.Create(&channel).Error)
	task := model.Task{TaskID: "video-confirmation", UserId: user.Id, ChannelId: channel.Id, Quota: 100, Status: model.TaskStatusFailure, SubmissionStage: "failed", SubmissionOwner: "worker", SubmissionLeaseUntil: common.GetTimestamp() + 60, PrivateData: model.TaskPrivateData{TokenId: token.Id, VideoSubmission: &model.VideoSubmission{Accounted: true}}}
	require.NoError(t, db.Create(&task).Error)
	latest := task.PrivateData
	latest.ResponsesBackground = true
	latest.PluginState = json.RawMessage(`{"round":2}`)
	require.NoError(t, db.Model(&model.Task{}).Where("id = ?", task.ID).Update("private_data", latest).Error)
	require.NoError(t, settleVideoSubmission(t.Context(), &task, "worker"))
	require.NotNil(t, task.PrivateData.SettledQuota)
	assert.Zero(t, *task.PrivateData.SettledQuota)
	assert.True(t, task.PrivateData.ResponsesBackground)
	assert.JSONEq(t, `{"round":2}`, string(task.PrivateData.PluginState))
	assert.Equal(t, "failed_final", task.SubmissionStage)
	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.NotNil(t, stored.PrivateData.SettledQuota)
	assert.Zero(t, *stored.PrivateData.SettledQuota)
	assert.JSONEq(t, `{"round":2}`, string(stored.PrivateData.PluginState))
}

func TestDeferredVideoSubmissionLifecycle(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.TaskEvent{}, &model.Channel{}, &model.Token{}))
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.TaskEvent{}, &model.Channel{}, &model.Token{}))
	user := model.User{Username: "async-worker", Quota: 9000, UsedQuota: 1000}
	require.NoError(t, db.Create(&user).Error)
	channel := model.Channel{Name: "async-worker", UsedQuota: 1000}
	require.NoError(t, db.Create(&channel).Error)
	token := model.Token{UserId: user.Id, Key: common.GetUUID(), RemainQuota: 9000, UsedQuota: 1000}
	require.NoError(t, db.Create(&token).Error)
	refund := model.Task{TaskID: model.GenerateTaskID(), UserId: user.Id, ChannelId: channel.Id, Quota: 1000, Status: model.TaskStatusFailure, SubmissionStage: "failed", SubmissionOwner: "refund-worker", SubmissionLeaseUntil: common.GetTimestamp() + 60, PrivateData: model.TaskPrivateData{TokenId: token.Id, VideoSubmission: &model.VideoSubmission{Accounted: true}}}
	require.NoError(t, db.Create(&refund).Error)
	previous, applied, err := model.AdjustVideoSubmissionQuota(t.Context(), refund.ID, "wrong-worker", 0)
	require.NoError(t, err)
	assert.False(t, applied)
	assert.Equal(t, 1000, previous)
	previous, applied, err = model.AdjustVideoSubmissionQuota(t.Context(), refund.ID, "refund-worker", 0)
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, 1000, previous)
	previous, applied, err = model.AdjustVideoSubmissionQuota(t.Context(), refund.ID, "refund-worker", 0)
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Zero(t, previous)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 10000, user.Quota)
	assert.Zero(t, user.UsedQuota)
	assert.Zero(t, user.RequestCount)
	assert.Zero(t, channel.UsedQuota)
	assert.Equal(t, 10000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	refund.ID, refund.TaskID, refund.Quota = 0, model.GenerateTaskID(), 1000
	require.NoError(t, db.Model(&user).Updates(map[string]any{"quota": 9000, "used_quota": 1000}).Error)
	require.NoError(t, db.Model(&token).Updates(map[string]any{"remain_quota": 9000, "used_quota": 1000}).Error)
	require.NoError(t, db.Delete(&channel).Error)
	require.NoError(t, db.Create(&refund).Error)
	_, applied, err = model.AdjustVideoSubmissionQuota(t.Context(), refund.ID, "refund-worker", 0)
	require.NoError(t, err)
	assert.True(t, applied, "a deleted channel cannot block wallet refunds")
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 10000, user.Quota)
	reservation := model.Task{TaskID: model.GenerateTaskID(), UserId: user.Id, ChannelId: channel.Id, Quota: 100, Status: model.TaskStatusQueued, PrivateData: model.TaskPrivateData{TokenId: token.Id, VideoSubmission: &model.VideoSubmission{TargetQuota: 100}}}
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("video_task_insert_failure", func(tx *gorm.DB) {
		if task, ok := tx.Statement.Dest.(*model.Task); ok && task.TaskID == reservation.TaskID {
			tx.AddError(fmt.Errorf("injected video task insert failure"))
		}
	}))
	reserveErr := model.ReserveVideoSubmission(t.Context(), &reservation, "wallet_only", false, false)
	require.ErrorContains(t, reserveErr, "injected video task insert failure")
	require.NoError(t, db.Callback().Create().Remove("video_task_insert_failure"))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 10000, user.Quota, "task persistence and wallet debit roll back together")
	assert.Equal(t, 10000, token.RemainQuota)
	var persisted int64
	require.NoError(t, db.Model(&model.Task{}).Where("task_id = ?", reservation.TaskID).Count(&persisted).Error)
	assert.Zero(t, persisted)
	require.NoError(t, db.AutoMigrate(&model.UserSubscription{}, &model.SubscriptionPlan{}))
	plan := model.SubscriptionPlan{Title: "async-test", TotalAmount: 5000, QuotaResetPeriod: "never"}
	require.NoError(t, db.Create(&plan).Error)
	subscription := model.UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: 5000, EndTime: common.GetTimestamp() + 3600, Status: "active"}
	require.NoError(t, db.Create(&subscription).Error)
	reservation.ID, reservation.TaskID = 0, model.GenerateTaskID()
	require.NoError(t, model.ReserveVideoSubmission(t.Context(), &reservation, "subscription_only", false, false))
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.EqualValues(t, 100, subscription.AmountUsed)
	assert.Equal(t, subscription.Id, reservation.PrivateData.SubscriptionId)
	assert.Equal(t, "subscription", reservation.PrivateData.BillingSource)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 10000, user.Quota, "subscription reservations must not debit the wallet")
	require.NoError(t, db.Model(&reservation).Updates(map[string]any{"submission_stage": "failed", "submission_owner": "sub-worker", "submission_lease_until": common.GetTimestamp() + 60}).Error)
	_, applied, err = model.AdjustVideoSubmissionQuota(t.Context(), reservation.ID, "sub-worker", 0)
	require.NoError(t, err)
	assert.True(t, applied)
	require.NoError(t, db.First(&subscription, subscription.Id).Error)
	assert.Zero(t, subscription.AmountUsed)
	task := &model.Task{TaskID: model.GenerateTaskID(), Status: model.TaskStatusQueued, Progress: "0%", SubmissionStage: "pending"}
	require.NoError(t, db.Create(task).Error)

	claimed, err := model.ClaimVideoSubmission(t.Context(), task.ID, "worker-one")
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = model.ClaimVideoSubmission(t.Context(), task.ID, "worker-two")
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Empty(t, model.GetAllUnFinishSyncTasks(100), "a local task must not be polled with its public ID")
	require.NoError(t, db.First(task, task.ID).Error)
	assert.Empty(t, task.GetUpstreamTaskID())
	require.NoError(t, db.Model(task).Update("submission_lease_until", common.GetTimestamp()-1).Error)
	claims := make(chan bool, 2)
	errors := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, worker := range []string{"worker-two", "worker-three"} {
		wg.Go(func() {
			<-start
			won, claimErr := model.ClaimVideoSubmission(t.Context(), task.ID, worker)
			claims <- won
			errors <- claimErr
		})
	}
	close(start)
	wg.Wait()
	wins := 0
	for range 2 {
		require.NoError(t, <-errors)
		if <-claims {
			wins++
		}
	}
	assert.Equal(t, 1, wins, "only one connection may resume an expired material preparation")

	require.NoError(t, db.Model(task).Updates(map[string]any{"submission_stage": "submitting", "submission_lease_until": common.GetTimestamp() - 1}).Error)
	claimed, err = model.ClaimVideoSubmission(t.Context(), task.ID, "worker-three")
	require.NoError(t, err)
	assert.False(t, claimed, "a possibly accepted upstream request must not be replayed")
	uncertain := model.Task{TaskID: model.GenerateTaskID(), UserId: user.Id, ChannelId: channel.Id, Status: model.TaskStatusQueued, SubmissionStage: "submitting", PrivateData: model.TaskPrivateData{VideoSubmission: &model.VideoSubmission{}}}
	require.NoError(t, db.Create(&uncertain).Error)
	require.NoError(t, runVideoSubmission(t.Context(), &uncertain, nil))
	require.NoError(t, db.First(&uncertain, uncertain.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, uncertain.Status)
	assert.Equal(t, "视频提交结果暂无法确认，请勿重复提交", uncertain.FailReason)
	assert.Equal(t, "failed_final", uncertain.SubmissionStage)
	t.Run("Redis and batch accounting", func(t *testing.T) {
		server := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: server.Addr()})
		oldRedis, oldClient, oldBatch := common.RedisEnabled, common.RDB, common.BatchUpdateEnabled
		common.RedisEnabled, common.RDB, common.BatchUpdateEnabled = true, client, true
		t.Cleanup(func() {
			common.RedisEnabled, common.RDB, common.BatchUpdateEnabled = oldRedis, oldClient, oldBatch
			require.NoError(t, client.Close())
		})
		localChannel := model.Channel{Name: "cache-accounting"}
		require.NoError(t, db.Create(&localChannel).Error)
		queued := model.Task{TaskID: model.GenerateTaskID(), UserId: user.Id, ChannelId: localChannel.Id, Quota: 100, Status: model.TaskStatusQueued, PrivateData: model.TaskPrivateData{TokenId: token.Id, VideoSubmission: &model.VideoSubmission{TargetQuota: 100}}}
		require.NoError(t, model.ReserveVideoSubmission(t.Context(), &queued, "wallet_only", false, false))
		quota, err := model.GetUserQuota(user.Id, false)
		require.NoError(t, err)
		assert.Equal(t, 9900, quota)
		cachedToken, err := model.GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		assert.Equal(t, 9900, cachedToken.RemainQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 9900, user.Quota, "batch mode cannot delay a durable task debit")
		require.NoError(t, db.Model(&queued).Updates(map[string]any{"submission_stage": "failed", "submission_owner": "cache-worker", "submission_lease_until": common.GetTimestamp() + 60}).Error)
		_, applied, err := model.AdjustVideoSubmissionQuota(t.Context(), queued.ID, "cache-worker", 0)
		require.NoError(t, err)
		assert.True(t, applied)
		quota, err = model.GetUserQuota(user.Id, false)
		require.NoError(t, err)
		assert.Equal(t, 10000, quota)
		cachedToken, err = model.GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		assert.Equal(t, 10000, cachedToken.RemainQuota)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, 10000, user.Quota)
	})
}

func TestLocalVideoSubmissionIsolation(t *testing.T) {
	db := setupMaterialConcurrentPostgres(t)
	previousLimit := constant.TaskQueryLimit
	constant.TaskQueryLimit = 100
	t.Cleanup(func() { constant.TaskQueryLimit = previousLimit })
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.TaskEvent{}, &model.Channel{}))
	channel := model.Channel{Name: "local video isolation", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	historical := model.Task{TaskID: model.GenerateTaskID(), ChannelId: channel.Id, Platform: "historical-video", Status: model.TaskStatusQueued, SubmissionStage: "pending", PrivateData: model.TaskPrivateData{VideoSubmission: &model.VideoSubmission{}}}
	require.NoError(t, db.Create(&historical).Error)
	historicalPoll := model.Task{TaskID: model.GenerateTaskID(), ChannelId: channel.Id, Platform: "historical-poll", Status: model.TaskStatusSubmitted, PrivateData: model.TaskPrivateData{UpstreamTaskID: "old-upstream", VideoSubmission: &model.VideoSubmission{}}}
	require.NoError(t, db.Create(&historicalPoll).Error)
	runner := localVideoSubmissionRunner{afterID: historicalPoll.ID}
	queued := model.Task{TaskID: model.GenerateTaskID(), ChannelId: channel.Id, Platform: "new-local-video", Status: model.TaskStatusQueued, SubmissionStage: "pending", PrivateData: model.TaskPrivateData{VideoSubmission: &model.VideoSubmission{}}}
	require.NoError(t, db.Create(&queued).Error)
	count, err := runVideoSubmissionPass(t.Context(), runner.afterID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	require.NoError(t, db.First(&historical, historical.ID).Error)
	assert.Equal(t, "pending", historical.SubmissionStage)
	assert.Empty(t, historical.SubmissionOwner)
	require.NoError(t, db.First(&queued, queued.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, queued.Status)
	assert.Equal(t, "视频请求处理失败，请联系管理员", queued.FailReason)
	synchronous := model.Task{TaskID: model.GenerateTaskID(), ChannelId: channel.Id, Platform: "synchronous-video", Status: model.TaskStatusSubmitted, PrivateData: model.TaskPrivateData{UpstreamTaskID: "synchronous-upstream"}}
	require.NoError(t, db.Create(&synchronous).Error)
	pollable := model.Task{TaskID: model.GenerateTaskID(), ChannelId: channel.Id, Platform: "new-local-poll", Status: model.TaskStatusNotStart, SubmitTime: common.GetTimestamp(), PrivateData: model.TaskPrivateData{UpstreamTaskID: "new-upstream", VideoSubmission: &model.VideoSubmission{}}}
	require.NoError(t, db.Create(&pollable).Error)
	constant.TaskQueryLimit = 1
	previousFactory := service.GetTaskAdaptorFunc
	t.Cleanup(func() { service.GetTaskAdaptorFunc = previousFactory })
	var platforms []constant.TaskPlatform
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		platforms = append(platforms, platform)
		return nil
	}
	require.NoError(t, runner.poll(t.Context()))
	require.NotEmpty(t, platforms)
	for _, platform := range platforms {
		assert.EqualValues(t, "new-local-poll", platform)
	}
	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })
	require.NoError(t, db.Model(&pollable).Update("submit_time", common.GetTimestamp()-120).Error)
	platforms = nil
	require.NoError(t, runner.poll(t.Context()))
	assert.Empty(t, platforms, "expired videos must fail locally without another provider query")
	require.NoError(t, db.First(&pollable, pollable.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, pollable.Status)
	assert.Equal(t, "failed_final", pollable.SubmissionStage)
	assert.Contains(t, pollable.FailReason, "任务超时")
	require.NoError(t, db.First(&historicalPoll, historicalPoll.ID).Error)
	assert.EqualValues(t, model.TaskStatusSubmitted, historicalPoll.Status)
	t.Setenv("LOCAL_VIDEO_SUBMISSION_AFTER_ID", fmt.Sprint(runner.afterID))
	previousMaster, previousUpdate := common.IsMasterNode, constant.UpdateTask
	t.Cleanup(func() { common.IsMasterNode, constant.UpdateTask = previousMaster, previousUpdate })
	common.IsMasterNode, constant.UpdateTask = true, false
	require.ErrorContains(t, StartLocalVideoSubmissionRunner(), "NODE_TYPE=slave and UPDATE_TASK=false")
	common.IsMasterNode, constant.UpdateTask = false, true
	require.ErrorContains(t, StartLocalVideoSubmissionRunner(), "NODE_TYPE=slave and UPDATE_TASK=false")
}

func TestMobileCloudVideoUploadBeforeSubmit(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	var operations []string
	var uploadedURLs []string
	assetPolls := make(map[string]int)
	materialServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/openapi-maas/exp/aicc/v2")
		switch {
		case r.Method == http.MethodPost && path == "/asset-group":
			operations = append(operations, "group")
			var input map[string]any
			if assert.NoError(t, common.DecodeJson(r.Body, &input)) {
				assert.NotContains(t, input, "description")
			}
			_, _ = w.Write([]byte(`{"state":"OK","body":{"groupId":"test-group","groupType":"AIGC"}}`))
		case r.Method == http.MethodPost && path == "/asset":
			var input service.MobileCloudCreateAssetInput
			if common.DecodeJson(r.Body, &input) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			operations = append(operations, "upload")
			uploadedURLs = append(uploadedURLs, input.URL)
			_, _ = fmt.Fprintf(w, `{"state":"OK","body":"test-asset-%d"}`, len(uploadedURLs))
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/asset/"):
			assetID := strings.TrimPrefix(path, "/asset/")
			assetPolls[assetID]++
			status := "PROCESSING"
			if assetPolls[assetID] > 1 {
				status = "ACTIVE"
			}
			operations = append(operations, status)
			_, _ = fmt.Fprintf(w, `{"state":"OK","body":{"assetId":%q,"groupId":"test-group","assetType":"Image","status":%q}}`, assetID, status)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer materialServer.Close()
	previousTransport := http.DefaultTransport
	transport := previousTransport.(*http.Transport).Clone()
	transport.DialTLSContext = (&tls.Dialer{Config: &tls.Config{RootCAs: materialServer.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}}).DialContext
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	service.InitHttpClient()
	channel := model.Channel{Name: "Mobile Cloud video mock", Type: constant.ChannelTypeTaskPlugin, Key: "test-key", Status: 1}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	config := service.MobileCloudMaterialConfiguration{ChannelID: channel.Id, Endpoint: materialServer.URL, AccessKey: "test-access", SecretKey: "test-secret", PoolID: "test-pool"}
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(config))
	var storedPlugin model.TaskPlugin
	require.NoError(t, db.Where("key = ? AND active = ? AND enabled = ?", "buluai-seedance", true, true).First(&storedPlugin).Error)
	plugin, err := pluginruntime.CompilePlugin(string(storedPlugin.Source), pluginruntime.Options{Key: storedPlugin.Key, Version: storedPlugin.Version})
	require.NoError(t, err)
	t.Run("native create returns only the public task ID", func(t *testing.T) {
		task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSubmitted, PrivateData: model.TaskPrivateData{UpstreamTaskID: "provider-task"}}
		task.SetData(map[string]any{"id": "provider-task", "upstream_id": "vendor-task", "model": "upstream-model", "status": "queued", "extra": "provider-metadata"})
		original := string(task.Data)
		view, err := service.BuildTaskPluginView(task)
		require.NoError(t, err)
		value, err := taskPluginProtocolJSONValue(view)
		require.NoError(t, err)
		response, err := plugin.Engine.CallPath(t.Context(), "native", []string{"taskCreated"}, map[string]any{}, value)
		require.NoError(t, err)
		encoded, err := common.Marshal(response)
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":"task_public"}`, string(encoded))
		assert.Equal(t, original, string(task.Data), "public presentation must preserve the internal upstream snapshot")
		assert.Equal(t, "provider-task", task.GetUpstreamTaskID())
	})
	prices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"AICC-Doubao-Seedance-2.0":0.01}`))
	imageURLs := make([]string, 7)
	content := []any{map[string]any{"type": "text", "text": "video with @图片1 through @图片7"}}
	expectedContent := []any{map[string]any{"type": "text", "text": "video with @图片1 through @图片7"}}
	for i := range imageURLs {
		imageURLs[i] = fmt.Sprintf("%s?image=%d", publicMaterialURL, i+1)
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURLs[i]}, "role": "reference_image"})
		expectedContent = append(expectedContent, map[string]any{"type": "image_url", "image_url": map[string]any{"url": fmt.Sprintf("asset://test-asset-%d", i+1)}, "role": "reference_image"})
	}
	videoCalls := 0
	upstreamStatus := http.StatusOK
	videoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		videoCalls++
		operations = append(operations, "video")
		assert.Equal(t, "/v1/contents/generations/tasks", r.URL.Path)
		for i := range imageURLs {
			assert.GreaterOrEqual(t, assetPolls[fmt.Sprintf("test-asset-%d", i+1)], 2)
		}
		var body map[string]any
		if assert.NoError(t, common.DecodeJson(r.Body, &body)) {
			assert.Equal(t, expectedContent, body["content"])
			assert.Equal(t, "doubao-seedance-2.0", body["model"])
			assert.Equal(t, float64(4), body["duration"])
			assert.Equal(t, "9:16", body["ratio"])
			assert.Equal(t, "480p", body["resolution"])
		}
		if upstreamStatus == 0 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if assert.NoError(t, err) {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamStatus)
		_, _ = w.Write([]byte(`{"id":"test-video"}`))
	}))
	defer videoServer.Close()
	require.NoError(t, db.Model(&channel).Update("base_url", videoServer.URL).Error)
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(config))
	decode := ""
	for _, route := range plugin.Meta.Routes {
		if route.Method == http.MethodPost && route.Path == "/v1/contents/generations/tasks" {
			decode = route.Decode
		}
	}
	require.NotEmpty(t, decode)
	otherChannel := model.Channel{Name: "Other BuluAI account", Type: channel.Type, Key: channel.Key, BaseURL: &videoServer.URL, Status: 1}
	require.NoError(t, db.Create(&otherChannel).Error)
	assetContent := expectedContent
	for _, tc := range []struct {
		name          string
		channel       *model.Channel
		lastSource    string
		failed        bool
		blocked       bool
		status        int
		lookupFailure bool
	}{
		{"upload seven images", &channel, imageURLs[6], false, false, http.StatusOK, false},
		{"reuse all seven images", &channel, imageURLs[6], false, false, http.StatusOK, false},
		{"foreign asset blocks batch", &channel, "asset://foreign-asset", false, true, http.StatusOK, false},
		{"failed seventh image blocks batch", &channel, imageURLs[6], true, true, http.StatusOK, false},
		{"upstream rejection releases reservations", &channel, imageURLs[6], false, false, http.StatusBadRequest, false},
		{"lost response is not replayed", &channel, imageURLs[6], false, false, 0, false},
		{"other BuluAI channel keeps original URLs", &otherChannel, imageURLs[6], false, false, http.StatusOK, false},
		{"material lookup failure blocks submission", &channel, imageURLs[6], false, true, http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.lookupFailure {
				intercepted := false
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("material_test_lookup_failure", func(tx *gorm.DB) {
					materials, ok := tx.Statement.Dest.(*[]model.Material)
					if ok && slices.ContainsFunc(*materials, func(material model.Material) bool { return material.SourceURL == tc.lastSource }) {
						intercepted = true
						tx.AddError(context.DeadlineExceeded)
					}
				}))
				t.Cleanup(func() {
					require.NoError(t, db.Callback().Query().Remove("material_test_lookup_failure"))
					assert.True(t, intercepted, "the material lookup failure must be injected")
				})
			}
			upstreamStatus = tc.status
			expectedContent = assetContent
			if tc.channel == &otherChannel {
				expectedContent = content
			}
			if tc.failed {
				var material model.Material
				require.NoError(t, db.Where("user_id = ? AND source_url = ?", users[1].Id, imageURLs[6]).First(&material).Error)
				require.NoError(t, db.Model(&material).Update("status", "failed").Error)
				require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", material.Id).Updates(map[string]any{"state": "failed", "upstream_status": "FAILED", "fail_reason": "Unsupported image format"}).Error)
				t.Cleanup(func() {
					require.NoError(t, db.Model(&material).Update("status", "ready").Error)
					require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", material.Id).Updates(map[string]any{"state": "ready", "upstream_status": "ACTIVE", "fail_reason": ""}).Error)
				})
			}
			requestContent := append([]any(nil), content...)
			requestContent[7] = map[string]any{"type": "image_url", "image_url": map[string]any{"url": tc.lastSource}, "role": "reference_image"}
			request := map[string]any{"model": "AICC-Doubao-Seedance-2.0", "duration": 4, "ratio": "9:16", "resolution": "480p", "content": requestContent}
			original, err := common.Marshal(request)
			require.NoError(t, err)
			value, err := plugin.Engine.CallMember(t.Context(), "native", decode, pluginruntime.RouteRequestContext{Method: http.MethodPost, Path: "/v1/contents/generations/tasks", Body: map[string]any{"kind": "json", "value": request}}.JSValue())
			require.NoError(t, err)
			intent, ok := value.(map[string]any)
			require.True(t, ok)
			action, ok := intent["action"].(string)
			require.True(t, ok)
			require.Equal(t, constant.TaskActionImageToVideo, action)
			c := newMaterialContext(httptest.NewRecorder(), http.MethodPost, "/v1/contents/generations/tasks", `{}`, users[1].Id)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "AICC-Doubao-Seedance-2.0")
			common.SetContextKey(c, constant.ContextKeyChannelId, tc.channel.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, tc.channel.Type)
			common.SetContextKey(c, constant.ContextKeyChannelModelMapping, `{"AICC-Doubao-Seedance-2.0":"doubao-seedance-2.0"}`)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, videoServer.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, tc.channel.Key)
			c.Set("task_request", intent["requestBody"])
			c.Set("task_action", action)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			events := []string{}
			billing := &taskSubmissionTestBilling{events: &events}
			info := &relaycommon.RelayInfo{UserId: users[1].Id, OriginModelName: "AICC-Doubao-Seedance-2.0", UserGroup: "default", UsingGroup: "default", Billing: billing, TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: action, LockedChannel: tc.channel}}
			before := videoCalls
			if tc.blocked || tc.status != http.StatusOK {
				outcome, taskErr := executeTaskSubmissionWith(c, info, relay.RelayTaskSubmit)
				assert.Nil(t, outcome)
				require.NotNil(t, taskErr)
				assert.True(t, taskErr.NoRetry)
				assert.Equal(t, 1, billing.refunds)
				assert.Equal(t, []string{"refund"}, events)
				if tc.blocked {
					assert.Equal(t, before, videoCalls)
					assert.Equal(t, "material_preparation_failed", taskErr.Code)
					issues, ok := taskErr.Data.([]service.MaterialPreparationIssue)
					require.True(t, ok)
					require.Len(t, issues, 1)
					assert.Equal(t, 7, issues[0].Index)
					assert.Equal(t, "content[7].image_url.url", issues[0].Field)
					assert.Equal(t, "reference_image", issues[0].Usage)
					if tc.lookupFailure {
						assert.Equal(t, "unavailable", issues[0].Status)
						assert.Equal(t, "视频请求处理失败，请联系管理员", issues[0].Reason)
					}
					if tc.failed {
						assert.Empty(t, issues[0].MaterialID)
						assert.Equal(t, "参考图片格式不支持，请更换图片", issues[0].Reason)
					}
				} else {
					assert.Equal(t, before+1, videoCalls)
				}
			} else {
				result, taskErr := relay.RelayTaskSubmit(c, info)
				require.Nil(t, taskErr, "%+v", taskErr)
				require.NotNil(t, result)
				assert.Equal(t, before+1, videoCalls)
			}
			unchanged, err := common.Marshal(request)
			require.NoError(t, err)
			assert.JSONEq(t, string(original), string(unchanged))
			var locked int64
			require.NoError(t, db.Model(&model.MaterialBinding{}).Where("channel_id = ? AND locked_by <> ?", channel.Id, "").Count(&locked).Error)
			assert.Zero(t, locked)
		})
	}
	assert.Equal(t, imageURLs, uploadedURLs)
	operationCounts := make(map[string]int)
	for _, operation := range operations {
		operationCounts[operation]++
	}
	assert.Equal(t, map[string]int{"group": 1, "upload": 7, "PROCESSING": 7, "ACTIVE": 7, "video": 5}, operationCounts)
	assert.Equal(t, 5, videoCalls)
}

func TestTopenRouterVideoUploadBeforeSubmit(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	previousMode, previousRetries := gin.Mode(), common.RetryTimes
	gin.SetMode(gin.TestMode)
	common.RetryTimes = 2
	t.Cleanup(func() { gin.SetMode(previousMode); common.RetryTimes = previousRetries })
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	operations := make(map[string]int)
	var uploadedURLs []string
	polls := make(map[string]int)
	rejectedAsset := ""
	materialServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/api/assets/", r.URL.Path)
		assert.Equal(t, "2024-01-01", r.URL.Query().Get("Version"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		var input map[string]any
		if !assert.NoError(t, common.DecodeJson(r.Body, &input)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		action := r.URL.Query().Get("Action")
		operations[action]++
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "CreateAssetGroup":
			assert.Equal(t, "AIGC", input["GroupType"])
			assert.NotContains(t, input, "Description")
			_, _ = w.Write([]byte(`{"Result":{"Id":"topen-group","GroupType":"AIGC"}}`))
		case "CreateAsset":
			assert.Equal(t, "topen-group", input["GroupId"])
			assert.Equal(t, "Image", input["AssetType"])
			source, ok := input["URL"].(string)
			assert.True(t, ok)
			uploadedURLs = append(uploadedURLs, source)
			_, _ = fmt.Fprintf(w, `{"Result":{"Id":"topen-asset-%d","Status":"Processing"}}`, len(uploadedURLs))
		case "GetAsset":
			id, ok := input["Id"].(string)
			assert.True(t, ok)
			polls[id]++
			if id == rejectedAsset {
				_, _ = fmt.Fprintf(w, `{"ResponseMetadata":{"Action":"GetAsset"},"Result":{"Id":%q,"GroupId":"topen-group","AssetType":"Image","Status":"Failed","Error":{"Code":"InputImageSensitiveContentDetected","Message":"The request failed because the input image may contain sensitive information; https://private.example/input?token=private-token; Authorization: Bearer test-token"}}}`, id)
				return
			}
			status := "Processing"
			if polls[id] > 1 {
				status = "Active"
			}
			operations[status]++
			_, _ = fmt.Fprintf(w, `{"Result":{"Id":%q,"GroupId":"topen-group","AssetType":"Image","Status":%q}}`, id, status)
		default:
			assert.Fail(t, "unexpected material action", "%s", action)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer materialServer.Close()
	previousTransport := http.DefaultTransport
	transport := previousTransport.(*http.Transport).Clone()
	transport.DialTLSContext = (&tls.Dialer{Config: &tls.Config{RootCAs: materialServer.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}}).DialContext
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	service.InitHttpClient()

	content := []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}, "role": "first_frame"},
		map[string]any{"type": "text", "text": "Keep @图片1 before @图片2; preserve text and order"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL + "?last"}, "role": "last_frame"},
	}
	expectedContent := []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://topen-asset-1"}, "role": "first_frame"},
		content[1],
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://topen-asset-2"}, "role": "last_frame"},
	}
	videoCalls := 0
	materialEnabled := true
	response := "ok"
	videoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		videoCalls++
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/contents/generations/tasks", r.URL.Path)
		assert.Equal(t, "Bearer video-account-key", r.Header.Get("Authorization"))
		if materialEnabled {
			assert.Equal(t, []string{publicMaterialURL, publicMaterialURL + "?last"}, uploadedURLs, "all images must upload before any video request")
			for _, id := range []string{"topen-asset-1", "topen-asset-2"} {
				assert.GreaterOrEqual(t, polls[id], 2, "every image must become Active before submission")
			}
		}
		wantContent := expectedContent
		if !materialEnabled {
			wantContent = content
		}
		var body map[string]any
		if assert.NoError(t, common.DecodeJson(r.Body, &body)) {
			assert.Equal(t, map[string]any{
				"model": "seedance-2.5", "content": wantContent, "duration": float64(4), "ratio": "9:16", "resolution": "480p",
				"seed": float64(0), "watermark": false, "generate_audio": false, "camera_fixed": false,
			}, body)
		}
		if materialEnabled {
			var materials []model.Material
			if assert.NoError(t, db.Where("user_id = ?", users[1].Id).Find(&materials).Error) {
				assert.Len(t, materials, 2)
				for _, material := range materials {
					_, err := model.QueueMaterialDeletion(users[1].Id, material.Id)
					assert.Error(t, err, "each material must stay reserved while the video request is in flight")
				}
			}
		}
		if response == "lost" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if assert.NoError(t, err) {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if response == "rejected" {
			w.WriteHeader(http.StatusBadRequest)
		}
		if response == "invalid" {
			_, _ = w.Write([]byte(`{}`)) // Accepted video, but local plugin parsing cannot obtain a task ID.
			return
		}
		_, _ = w.Write([]byte(`{"id":"topen-video"}`))
	}))
	defer videoServer.Close()
	channel := model.Channel{Name: "Explicitly bound TopenRouter account", Type: constant.ChannelTypeTaskPlugin, Key: "video-account-key", BaseURL: &videoServer.URL, Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, service.SaveTopenRouterMaterialConfiguration(service.TopenRouterMaterialConfiguration{ChannelID: channel.Id, Endpoint: materialServer.URL, Token: "test-token"}))

	// The unmodified plugin stands in for the configured video protocol, not TopenRouter's API contract.
	source, err := builtinplugins.Source("mobile-cloud")
	require.NoError(t, err)
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{Key: "mobile-cloud"})
	require.NoError(t, err)
	require.NoError(t, db.Where("key = ?", plugin.Meta.Key).Delete(&model.TaskPlugin{}).Error)
	installed := model.TaskPlugin{Key: plugin.Meta.Key, Version: plugin.Meta.Version, APIVersion: 1, Source: model.LongText(source), Active: true, Enabled: true}
	require.NoError(t, model.SaveTaskPlugin(&installed))
	require.NoError(t, db.First(&installed, installed.Id).Error)
	plugin, err = pluginruntime.CompilePlugin(string(installed.Source), pluginruntime.Options{Key: installed.Key, Version: installed.Version})
	require.NoError(t, err)
	decode, nativePath := "", ""
	for _, route := range plugin.Meta.Routes {
		if route.Method == http.MethodPost && route.Type == "submit" {
			decode, nativePath = route.Decode, route.Path
		}
	}
	require.NotEmpty(t, decode)
	prices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"seedance-2.0":0.01}`))

	for _, tc := range []struct {
		name     string
		response string
		failed   bool
		disabled bool
	}{
		{"disabled integration submits without uploading", "ok", false, true},
		{"upload every image before video", "ok", false, false},
		{"disabled integration preserves prepared materials", "ok", false, true},
		{"retry reuses successful uploads and group", "ok", false, false},
		{"failed last frame blocks the whole batch", "ok", true, false},
		{"video rejection releases reservations and refunds", "rejected", false, false},
		{"local response parse failure releases reservations and refunds", "invalid", false, false},
		{"lost video response is not replayed and refunds", "lost", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			materialEnabled = !tc.disabled
			config, err := service.LoadTopenRouterMaterialConfiguration()
			require.NoError(t, err)
			config.Enabled = common.GetPointer(materialEnabled)
			require.NoError(t, service.SaveTopenRouterMaterialConfiguration(*config))
			if tc.disabled {
				queries := 0
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("topen_toggle_no_queries", func(tx *gorm.DB) {
					if tx.Statement.Table == "materials" || tx.Statement.Table == "material_bindings" || tx.Statement.Table == "material_groups" {
						queries++
					}
				}))
				t.Cleanup(func() {
					assert.Zero(t, queries, "disabled integration must not prepare materials")
					require.NoError(t, db.Callback().Query().Remove("topen_toggle_no_queries"))
				})
			}
			response = tc.response
			beforeGroup, beforeUpload, beforePoll := operations["CreateAssetGroup"], operations["CreateAsset"], operations["GetAsset"]
			var failed model.Material
			if tc.failed {
				require.NoError(t, db.Where("user_id = ? AND source_url = ?", users[1].Id, publicMaterialURL+"?last").First(&failed).Error)
				require.NoError(t, db.Model(&failed).Update("status", "failed").Error)
				require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ? AND channel_id = ?", failed.Id, channel.Id).Updates(map[string]any{"state": "failed", "upstream_status": "FAILED", "fail_reason": "Unsupported image format"}).Error)
				t.Cleanup(func() {
					require.NoError(t, db.Model(&failed).Update("status", "ready").Error)
					require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ? AND channel_id = ?", failed.Id, channel.Id).Updates(map[string]any{"state": "ready", "upstream_status": "ACTIVE", "fail_reason": ""}).Error)
				})
			}
			request := map[string]any{"model": "seedance-2.0", "content": content, "duration": 4, "ratio": "9:16", "resolution": "480p", "seed": 0, "watermark": false, "generate_audio": false, "camera_fixed": false}
			original, err := common.Marshal(request)
			require.NoError(t, err)
			value, err := plugin.Engine.CallMember(t.Context(), "native", decode, pluginruntime.RouteRequestContext{Method: http.MethodPost, Path: nativePath, Body: map[string]any{"kind": "json", "value": request}}.JSValue())
			require.NoError(t, err)
			intent, ok := value.(map[string]any)
			require.True(t, ok)
			action, ok := intent["action"].(string)
			require.True(t, ok)
			require.Equal(t, constant.TaskActionImageToVideo, action)
			originalIntent, err := common.Marshal(intent["requestBody"])
			require.NoError(t, err)
			c := newMaterialContext(httptest.NewRecorder(), http.MethodPost, nativePath, string(original), users[1].Id)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "seedance-2.0")
			common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
			common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, videoServer.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, channel.Key)
			common.SetContextKey(c, constant.ContextKeyChannelModelMapping, `{"seedance-2.0":"seedance-2.5"}`)
			c.Set("task_request", intent["requestBody"])
			c.Set("task_action", action)
			c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
			events := []string{}
			billing := &taskSubmissionTestBilling{events: &events}
			info := &relaycommon.RelayInfo{UserId: users[1].Id, OriginModelName: "seedance-2.0", UserGroup: "default", UsingGroup: "default", Billing: billing, TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: action, LockedChannel: &channel}}
			before := videoCalls
			if tc.failed || tc.response != "ok" {
				outcome, taskErr := executeTaskSubmissionWith(c, info, relay.RelayTaskSubmit)
				assert.Nil(t, outcome)
				require.NotNil(t, taskErr)
				assert.True(t, taskErr.NoRetry)
				assert.Equal(t, 1, billing.refunds)
				assert.Equal(t, []string{"refund"}, events)
				if tc.failed {
					assert.Equal(t, before, videoCalls, "one failed item must prevent ANY video request")
					assert.Equal(t, "material_preparation_failed", taskErr.Code)
					issues, ok := taskErr.Data.([]service.MaterialPreparationIssue)
					require.True(t, ok)
					assert.Equal(t, "参考图片（第 3 项）格式不支持，请更换图片", taskErr.Message)
					assert.Equal(t, []service.MaterialPreparationIssue{{Index: 2, Field: "content[2].image_url.url", Usage: "last_frame", Status: "failed", Reason: "参考图片格式不支持，请更换图片"}}, issues)
				} else {
					assert.Equal(t, before+1, videoCalls, "even an accepted request with a lost or invalid response must not be replayed")
					assert.NotEqual(t, "material_preparation_failed", taskErr.Code)
					if tc.response == "lost" {
						assert.Equal(t, "do_request_failed", taskErr.Code)
					}
					if tc.response == "lost" || tc.response == "invalid" {
						assert.Equal(t, "视频提交结果暂无法确认，请勿重复提交", taskErr.Message)
					} else {
						assert.Equal(t, "视频请求处理失败，请联系管理员", taskErr.Message)
					}
				}
			} else {
				result, taskErr := relay.RelayTaskSubmit(c, info)
				require.Nil(t, taskErr, "%+v", taskErr)
				require.NotNil(t, result)
				assert.Equal(t, before+1, videoCalls)
				assert.Zero(t, billing.refunds)
				upstream, ok := common.GetContextKeyType[json.RawMessage](c, constant.ContextKeyTaskEventUpstreamRequest)
				if tc.disabled {
					assert.False(t, ok)
				} else {
					require.True(t, ok)
					expected, err := common.Marshal(map[string]any{
						"model": "seedance-2.5", "content": expectedContent, "duration": 4, "ratio": "9:16", "resolution": "480p",
						"seed": 0, "watermark": false, "generate_audio": false, "camera_fixed": false,
					})
					require.NoError(t, err)
					assert.JSONEq(t, string(expected), string(upstream))
				}
			}
			unchanged, err := common.Marshal(request)
			require.NoError(t, err)
			assert.JSONEq(t, string(original), string(unchanged), "caller content must remain unchanged, including on partial failure")
			unchangedIntent, err := common.Marshal(intent["requestBody"])
			require.NoError(t, err)
			assert.JSONEq(t, string(originalIntent), string(unchangedIntent), "the decoded body must remain reusable for retries")
			if tc.disabled {
				assert.Equal(t, beforeGroup, operations["CreateAssetGroup"])
				assert.Equal(t, beforeUpload, operations["CreateAsset"])
				assert.Equal(t, beforePoll, operations["GetAsset"])
				return
			}
			var bindings []model.MaterialBinding
			require.NoError(t, db.Where("channel_id = ? AND material_id IN (?)", channel.Id, db.Model(&model.Material{}).Select("id").Where("user_id = ?", users[1].Id)).Order("id").Find(&bindings).Error)
			require.Len(t, bindings, 2)
			for _, binding := range bindings {
				assert.Equal(t, "topenrouter", binding.Provider)
				assert.Empty(t, binding.LockedBy)
				assert.Zero(t, binding.LockedUntil)
			}
			var active int64
			require.NoError(t, db.Model(&model.MaterialUseLease{}).Where("binding_id IN (?)", db.Model(&model.MaterialBinding{}).Select("id").Where("channel_id = ?", channel.Id)).Count(&active).Error)
			assert.Zero(t, active, "all success/failure exits must release shared reservations")
			assert.Equal(t, 1, operations["CreateAssetGroup"])
			assert.Equal(t, 2, operations["CreateAsset"], "retry must not duplicate successful uploads")
		})
	}
	assert.Equal(t, 7, videoCalls)
	assert.Equal(t, map[string]int{"CreateAssetGroup": 1, "CreateAsset": 2, "GetAsset": 4, "Processing": 2, "Active": 2}, operations)
	t.Run("review rejection identifies the image in API errors", func(t *testing.T) {
		rejectedAsset = "topen-asset-2"
		defer func() { rejectedAsset = "" }()
		var failed model.Material
		require.NoError(t, db.Where("user_id = ? AND source_url = ?", users[1].Id, publicMaterialURL+"?last").First(&failed).Error)
		require.NoError(t, db.Model(&failed).Update("status", "processing").Error)
		require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ? AND channel_id = ?", failed.Id, channel.Id).Updates(map[string]any{"state": "processing", "upstream_status": "PROCESSING"}).Error)
		renderedPlugin, err := pluginruntime.CompilePlugin(source+"\nnative.error = function(ctx, error) { return {code: error.code, message: error.message}; };", pluginruntime.Options{Key: "mobile-cloud"})
		require.NoError(t, err)
		beforeVideo, beforeUpload, beforeGroup := videoCalls, operations["CreateAsset"], operations["CreateAssetGroup"]
		for _, surface := range []string{"openai_video", "native message-only renderer"} {
			t.Run(surface, func(t *testing.T) {
				request := map[string]any{"model": "seedance-2.0", "content": content}
				recorder := httptest.NewRecorder()
				path := "/v1/videos"
				if surface != "openai_video" {
					path = nativePath
				}
				c := newMaterialContext(recorder, http.MethodPost, path, "", users[1].Id)
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "seedance-2.0")
				common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
				common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, videoServer.URL)
				common.SetContextKey(c, constant.ContextKeyChannelKey, channel.Key)
				c.Set("task_request", request)
				c.Set("task_action", constant.TaskActionImageToVideo)
				c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Plugin: plugin})
				if surface != "openai_video" {
					c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: renderedPlugin})
					c.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Method: http.MethodPost, Path: path})
				}
				events := []string{}
				info := &relaycommon.RelayInfo{UserId: users[1].Id, OriginModelName: "seedance-2.0", UserGroup: "default", UsingGroup: "default", Billing: &taskSubmissionTestBilling{events: &events}, TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionImageToVideo}}
				result, taskErr := relay.RelayTaskSubmit(c, info)
				assert.Nil(t, result)
				require.NotNil(t, taskErr)
				assert.Equal(t, "material_preparation_failed", taskErr.Code)
				assert.True(t, taskErr.NoRetry)
				issues, ok := taskErr.Data.([]service.MaterialPreparationIssue)
				require.True(t, ok)
				require.Len(t, issues, 1)
				assert.Equal(t, 2, issues[0].Index)
				assert.Equal(t, "content[2].image_url.url", issues[0].Field)
				assert.Equal(t, "last_frame", issues[0].Usage)
				assert.Empty(t, issues[0].MaterialID)
				assert.Equal(t, "failed", issues[0].Status)
				assert.Equal(t, "参考图片未通过审核，请更换图片", issues[0].Reason)
				respondTaskSubmissionError(c, taskErr)
				assert.Equal(t, http.StatusBadRequest, recorder.Code)
				var body struct {
					Message string                             `json:"message"`
					Data    []service.MaterialPreparationIssue `json:"data"`
				}
				require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
				assert.Equal(t, "参考图片（第 3 项）未通过审核，请更换图片", body.Message)
				assert.NotContains(t, recorder.Body.String(), failed.MaterialID)
				assert.NotContains(t, recorder.Body.String(), "InputImageSensitiveContentDetected")
				if surface == "openai_video" {
					assert.Equal(t, issues, body.Data)
				}
				for _, private := range []string{"test-token", "private-token", "private.example", publicMaterialURL, "/input"} {
					assert.NotContains(t, recorder.Body.String(), private)
				}
			})
		}
		var stored model.MaterialBinding
		require.NoError(t, db.Where("material_id = ? AND channel_id = ?", failed.Id, channel.Id).First(&stored).Error)
		assert.Equal(t, "failed", stored.State)
		assert.Equal(t, "FAILED", stored.UpstreamStatus)
		assert.Contains(t, stored.FailReason, "InputImageSensitiveContentDetected")
		assert.Equal(t, "topen-asset-2", stored.AssetID)
		assert.Equal(t, "topen-group", stored.GroupID)
		assert.Equal(t, beforeVideo, videoCalls)
		assert.Equal(t, beforeUpload, operations["CreateAsset"])
		assert.Equal(t, beforeGroup, operations["CreateAssetGroup"])
	})
}

func TestTopenRouterVideoBodyChannelBinding(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	channel := model.Channel{Name: "Bound video destination", Type: constant.ChannelTypeTaskPlugin, Key: "separate-video-key", BaseURL: common.GetPointer("https://video.invalid"), Status: common.ChannelStatusEnabled}
	other := model.Channel{Name: "Unrelated video destination", Type: channel.Type, Key: channel.Key, BaseURL: channel.BaseURL, Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&other).Error)
	var before model.Channel
	require.NoError(t, db.First(&before, channel.Id).Error)
	var channelCount int64
	require.NoError(t, db.Model(&model.Channel{}).Count(&channelCount).Error)
	config := service.TopenRouterMaterialConfiguration{ChannelID: channel.Id, Endpoint: "https://materials.invalid", Token: "test-token"}
	require.NoError(t, service.SaveTopenRouterMaterialConfiguration(config))
	loaded, err := service.LoadTopenRouterMaterialConfiguration()
	require.NoError(t, err)
	assert.Equal(t, config.Token, loaded.Token, "administrators explicitly bind independent material and video accounts")
	assert.Equal(t, config.Endpoint, loaded.Endpoint)
	require.Equal(t, service.MobileCloudVideoChannelFingerprint(&channel), loaded.ChannelFingerprint)
	var stored model.Option
	require.NoError(t, db.Where("key = ?", model.TopenRouterMaterialConfigOption).First(&stored).Error)
	assert.True(t, strings.HasPrefix(stored.Value, fmt.Sprintf("v2.%d.", channel.Id)))
	assert.NotContains(t, stored.Value, loaded.ChannelFingerprint)
	assert.NotContains(t, stored.Value, config.Token)
	var after model.Channel
	require.NoError(t, db.First(&after, channel.Id).Error)
	assert.Equal(t, before, after, "material configuration must not mutate the selected video channel")
	var afterCount int64
	require.NoError(t, db.Model(&model.Channel{}).Count(&afterCount).Error)
	assert.Equal(t, channelCount, afterCount, "material configuration must not create channels")
	textBody := map[string]any{"content": []any{map[string]any{"type": "text", "text": "video"}}, "seed": 0, "watermark": false}
	prepared, issues, reservation, err := service.PrepareTopenRouterVideoBody(t.Context(), users[1].Id, &channel, textBody)
	require.NoError(t, err)
	assert.Equal(t, textBody, prepared)
	assert.Empty(t, issues)
	assert.Nil(t, reservation)

	for _, tc := range []struct {
		name   string
		change func(*model.Channel)
	}{
		{"key", func(ch *model.Channel) { ch.Key = "changed-video-account" }},
		{"base_url", func(ch *model.Channel) { ch.BaseURL = common.GetPointer("https://changed-video.invalid") }},
		{"header", func(ch *model.Channel) { ch.HeaderOverride = common.GetPointer(`{"X-Account":"changed"}`) }},
		{"multikey", func(ch *model.Channel) { ch.ChannelInfo.IsMultiKey = true }},
		{"disabled", func(ch *model.Channel) { ch.Status = common.ChannelStatusManuallyDisabled }},
	} {
		for _, location := range []string{"request", "database"} {
			if tc.name == "disabled" && location == "request" {
				continue // Relay snapshots do not carry status; the database must enforce it.
			}
			t.Run(location+"/"+tc.name, func(t *testing.T) {
				changed := channel
				tc.change(&changed)
				target := &changed
				if location == "database" {
					target = &channel
					require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Select("key", "base_url", "header_override", "channel_info", "status").Updates(&changed).Error)
					t.Cleanup(func() {
						require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Select("key", "base_url", "header_override", "channel_info", "status").Updates(&channel).Error)
					})
				}
				prepared, _, reservation, err := service.PrepareTopenRouterVideoBody(t.Context(), users[1].Id, target, textBody)
				require.Error(t, err, "changed or unsafe channel bindings must fail before any network request")
				assert.Nil(t, prepared)
				assert.Nil(t, reservation)
			})
		}
	}
	imageBody := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}, "role": "reference_image"}}, "seed": 0, "watermark": false}
	for _, failure := range []string{"healthy", "corrupted ciphertext", "missing cipher"} {
		t.Run("unrelated channel/"+failure, func(t *testing.T) {
			if failure == "corrupted ciphertext" {
				require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", fmt.Sprintf("v2.%d.invalid", channel.Id)).Error)
				t.Cleanup(func() {
					require.NoError(t, db.Model(&model.Option{}).Where("key = ?", stored.Key).Update("value", stored.Value).Error)
				})
			}
			if failure == "missing cipher" {
				t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "")
				t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY_FILE", filepath.Join(t.TempDir(), "missing.key"))
			}
			prepared, issues, reservation, err := service.PrepareTopenRouterVideoBody(t.Context(), users[1].Id, &other, imageBody)
			require.NoError(t, err, "a known unrelated v2 binding must bypass decryption and preserve original URLs")
			assert.Equal(t, imageBody, prepared)
			assert.Equal(t, publicMaterialURL, imageBody["content"].([]any)[0].(map[string]any)["image_url"].(map[string]any)["url"])
			assert.Empty(t, issues)
			assert.Nil(t, reservation)
			if failure != "healthy" {
				prepared, _, reservation, err = service.PrepareTopenRouterVideoBody(t.Context(), users[1].Id, &channel, imageBody)
				require.Error(t, err, "the bound channel must fail closed")
				assert.Nil(t, prepared)
				assert.Nil(t, reservation)
			}
			var count int64
			require.NoError(t, db.Model(&model.Material{}).Where("user_id = ?", users[1].Id).Count(&count).Error)
			assert.Zero(t, count, "unrelated channels and invalid bindings must not queue uploads")
		})
	}
	t.Run("explicit rebinding isolates old resource mappings", func(t *testing.T) {
		oldScope := service.TopenRouterMaterialScope(loaded)
		require.NoError(t, db.Model(&channel).Update("key", "replacement-video-account").Error)
		config.Token = "" // Preserve the independent material credential on an explicit admin rebind.
		require.NoError(t, service.SaveTopenRouterMaterialConfiguration(config))
		rebound, err := service.LoadTopenRouterMaterialConfiguration()
		require.NoError(t, err)
		assert.Equal(t, loaded.Token, rebound.Token)
		assert.Equal(t, loaded.Endpoint, rebound.Endpoint)
		assert.NotEqual(t, loaded.ChannelFingerprint, rebound.ChannelFingerprint)
		assert.NotEqual(t, oldScope, service.TopenRouterMaterialScope(rebound), "scope must include the encrypted video-channel fingerprint")
		prepared, issues, reservation, err := service.PrepareTopenRouterVideoBody(t.Context(), users[1].Id, &channel, textBody)
		require.NoError(t, err)
		assert.Equal(t, textBody, prepared)
		assert.Empty(t, issues)
		assert.Nil(t, reservation)
	})
}

func TestVideoMaterialSourceReuse(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	provider := &materialPreparationProvider{status: "ACTIVE"}
	channel := service.MaterialChannel{ChannelID: 5, ChannelName: "Mobile Cloud", Provider: "mobile_cloud", Scope: "video-scope", Client: provider}
	inputs := []service.MaterialImageInput{
		{Index: 1, Source: publicMaterialURL, Field: "content[1].image_url.url", Usage: "first_frame"},
		{Index: 3, Source: publicMaterialURL, Field: "content[3].image_url.url", Usage: "reference_image"},
	}
	for range 2 {
		resolved, issues, err := service.PrepareVideoMaterials(context.Background(), users[1].Id, channel, inputs)
		require.NoError(t, err)
		require.Empty(t, issues)
		assert.Equal(t, []string{"asset://upstream-asset", "asset://upstream-asset"}, resolved)
	}
	assert.Equal(t, 1, provider.assets, "duplicate URLs and later requests reuse the same upstream asset")
	var materials []model.Material
	require.NoError(t, db.Where("user_id = ? AND source_url = ?", users[1].Id, publicMaterialURL).Find(&materials).Error)
	require.Len(t, materials, 1)
	var binding model.MaterialBinding
	require.NoError(t, db.Where("material_id = ? AND scope = ?", materials[0].Id, channel.Scope).First(&binding).Error)
	assert.Equal(t, channel.ChannelID, binding.ChannelID)
	assert.Equal(t, "ACTIVE", binding.UpstreamStatus)

	t.Run("duplicate references reserve once with changed-row semantics", func(t *testing.T) {
		require.NoError(t, db.Exec(`CREATE FUNCTION material_test_skip_unchanged() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NULL; END IF; RETURN NEW; END $$`).Error)
		require.NoError(t, db.Exec(`CREATE TRIGGER material_test_skip_unchanged BEFORE UPDATE ON material_bindings FOR EACH ROW EXECUTE FUNCTION material_test_skip_unchanged()`).Error)
		_, err := model.ReserveVideoMaterials(db, users[1].Id, channel.Scope, "test-submission", []string{binding.AssetID, binding.AssetID})
		require.NoError(t, err)
		_, err = model.QueueMaterialDeletion(users[1].Id, materials[0].Id)
		assert.Error(t, err)
		require.NoError(t, model.ReleaseVideoMaterials(db, "test-submission"))
	})

	resolved, issues, err := service.PrepareVideoMaterials(context.Background(), users[1].Id, channel, []service.MaterialImageInput{{Source: "asset://upstream-asset", Field: "content[0].image_url.url"}})
	require.NoError(t, err)
	require.Empty(t, issues)
	assert.Equal(t, []string{"asset://upstream-asset"}, resolved)
	assert.Equal(t, 1, provider.assets)

	for _, tc := range []struct {
		name  string
		user  int
		scope string
	}{
		{"other user", users[2].Id, channel.Scope},
		{"other resource space", users[1].Id, "another-video-scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolated := channel
			isolated.Scope = tc.scope
			resolved, issues, err := service.PrepareVideoMaterials(context.Background(), tc.user, isolated, []service.MaterialImageInput{{Source: "asset://upstream-asset"}})
			require.NoError(t, err)
			assert.Nil(t, resolved)
			require.Len(t, issues, 1)
			assert.Equal(t, "unavailable", issues[0].Status)
		})
	}
}

func TestVideoMaterialChannelIsolation(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	group := model.MaterialGroup{UserId: users[1].Id, Name: "other channel group"}
	require.NoError(t, db.Create(&group).Error)
	original := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "other channel image", GroupId: &group.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"}
	require.NoError(t, db.Create(&original).Error)
	originalBinding := model.MaterialBinding{MaterialId: original.Id, ChannelID: 5, Scope: "channel-a", Version: model.MaterialSourceVersion(&original), AssetID: "channel-a-asset", GroupID: "channel-a-group", State: "ready", UpstreamStatus: "ACTIVE"}
	require.NoError(t, db.Create(&originalBinding).Error)
	provider := &materialPreparationProvider{status: "ACTIVE"}
	channel := service.MaterialChannel{ChannelID: 10, Scope: "channel-b", Client: provider}
	inputs := []service.MaterialImageInput{{Index: 1, Source: publicMaterialURL, Field: "content[1].image_url.url", Usage: "reference_image"}, {Index: 2, Source: publicMaterialURL, Field: "content[2].image_url.url", Usage: "last_frame"}}
	for range 2 {
		resolved, issues, err := service.PrepareVideoMaterials(t.Context(), users[1].Id, channel, inputs)
		require.NoError(t, err)
		require.Empty(t, issues)
		assert.Equal(t, []string{"asset://upstream-asset", "asset://upstream-asset"}, resolved)
	}
	assert.Equal(t, 1, provider.assets, "duplicate images and retries reuse only the selected channel's upload")
	assert.Equal(t, 1, provider.groups)
	assert.Equal(t, "upstream-group", provider.input.GroupID)
	var current []model.Material
	require.NoError(t, db.Where("user_id = ? AND source_url = ?", users[1].Id, publicMaterialURL).Order("id").Find(&current).Error)
	require.Len(t, current, 2)
	assert.Equal(t, original.Id, current[0].Id)
	assert.Equal(t, original.GroupId, current[0].GroupId)
	assert.Nil(t, current[1].GroupId, "new channel must not inherit another channel's local group")
	var mapping model.MaterialBinding
	require.NoError(t, db.Where("material_id = ? AND scope = ?", current[1].Id, channel.Scope).First(&mapping).Error)
	assert.Equal(t, channel.ChannelID, mapping.ChannelID)
	assert.Equal(t, "upstream-asset", mapping.AssetID)
	assert.Equal(t, "upstream-group", mapping.GroupID)
	var unchanged model.MaterialBinding
	require.NoError(t, db.First(&unchanged, originalBinding.Id).Error)
	assert.Equal(t, originalBinding, unchanged)
	resolved, issues, err := service.PrepareVideoMaterials(t.Context(), users[1].Id, channel, []service.MaterialImageInput{{Source: "asset://channel-a-asset"}})
	require.NoError(t, err)
	assert.Nil(t, resolved)
	require.Len(t, issues, 1)
	assert.Equal(t, "unavailable", issues[0].Status)
	assert.Equal(t, 1, provider.assets)
	_, err = model.ReserveVideoMaterials(db, users[1].Id, channel.Scope, "wrong-channel", []string{originalBinding.AssetID})
	assert.Error(t, err, "another channel's asset cannot be reserved for submission")
	t.Run("identical upstream identifiers remain channel scoped", func(t *testing.T) {
		require.NoError(t, db.Model(&originalBinding).Update("asset_id", mapping.AssetID).Error)
		resolved, issues, err := service.PrepareVideoMaterials(t.Context(), users[1].Id, channel, []service.MaterialImageInput{{Source: "asset://" + mapping.AssetID}})
		require.NoError(t, err)
		require.Empty(t, issues)
		assert.Equal(t, []string{"asset://" + mapping.AssetID}, resolved)
		_, err = model.ReserveVideoMaterials(db, users[1].Id, channel.Scope, "channel-b-submit", []string{mapping.AssetID})
		require.NoError(t, err)
		var reserved []model.MaterialUseLease
		require.NoError(t, db.Where("owner = ?", "channel-b-submit").Find(&reserved).Error)
		require.Len(t, reserved, 1)
		assert.Equal(t, mapping.Id, reserved[0].BindingId)
		require.NoError(t, db.First(&unchanged, originalBinding.Id).Error)
		assert.Empty(t, unchanged.LockedBy)
		assert.Equal(t, 1, provider.assets)
	})
}

func TestVideoMaterialSkipsUnconfiguredChannels(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	channels := []model.Channel{
		{Name: "mobile account", Key: "mobile-key", Status: 1},
		{Name: "topenrouter account", Key: "topenrouter-key", Status: 1},
		{Name: "unconfigured video account", Key: "video-key", Status: 1},
	}
	require.NoError(t, db.Create(&channels).Error)
	body := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": publicMaterialURL}}}, "watermark": false, "seed": 0}
	t.Run("no material integrations", func(t *testing.T) {
		prepared, issues, reservation, err := service.PrepareVideoMaterialRequest(t.Context(), users[1].Id, &channels[2], body)
		require.NoError(t, err)
		assert.Equal(t, body, prepared)
		assert.Empty(t, issues)
		assert.Nil(t, reservation)
	})
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{ChannelID: channels[0].Id, Endpoint: service.MobileCloudMaterialDefaultEndpoint, AccessKey: "test-access", SecretKey: "test-secret", PoolID: "test-pool"}))
	require.NoError(t, service.SaveTopenRouterMaterialConfiguration(service.TopenRouterMaterialConfiguration{ChannelID: channels[1].Id, Endpoint: service.TopenRouterMaterialDefaultEndpoint, Token: "test-token"}))
	t.Run("only other model channels configured", func(t *testing.T) {
		prepared, issues, reservation, err := service.PrepareVideoMaterialRequest(t.Context(), users[1].Id, &channels[2], body)
		require.NoError(t, err)
		assert.Equal(t, body, prepared)
		assert.Empty(t, issues)
		assert.Nil(t, reservation)
	})
	var count int64
	require.NoError(t, db.Model(&model.Material{}).Where("user_id = ?", users[1].Id).Count(&count).Error)
	assert.Zero(t, count, "unconfigured channels must never queue image uploads")
}

func TestVideoMaterialBackgroundSyncStaysInSelectedChannel(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	original := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"}
	require.NoError(t, db.Create(&original).Error)
	originalBinding := model.MaterialBinding{MaterialId: original.Id, ChannelID: 5, Scope: "channel-a", Version: model.MaterialSourceVersion(&original), AssetID: "channel-a-asset", GroupID: "channel-a-group", State: "ready", UpstreamStatus: "ACTIVE"}
	require.NoError(t, db.Create(&originalBinding).Error)
	material, err := model.FindOrCreateVideoMaterial(db, users[1].Id, publicMaterialURL, "channel-b")
	require.NoError(t, err)
	require.NotEqual(t, original.Id, material.Id)
	other := &materialPreparationProvider{status: "ACTIVE"}
	selected := &materialPreparationProvider{status: "PROCESSING"}
	channels := []service.MaterialChannel{
		{ChannelID: 5, Scope: "channel-a", Client: other},
		{ChannelID: 10, Scope: "channel-b", Client: selected},
	}
	t.Run("unbound records never broadcast an initial upload", func(t *testing.T) {
		unbound := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?unbound=true", Status: "pending"}
		require.NoError(t, db.Create(&unbound).Error)
		require.NoError(t, service.SyncMaterial(t.Context(), &unbound, channels))
		var count int64
		require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", unbound.Id).Count(&count).Error)
		assert.Zero(t, count)
		assert.Zero(t, other.groups)
		assert.Zero(t, other.assets)
		assert.Zero(t, selected.groups)
		assert.Zero(t, selected.assets)
		var queued []model.Material
		require.NoError(t, service.MaterialPreparationQueue(db, []string{channels[0].Scope, channels[1].Scope}).Where("id = ?", unbound.Id).Find(&queued).Error)
		assert.Empty(t, queued, "unbound materials wait for a model channel rather than background upload")
	})
	require.NoError(t, service.SyncMaterial(t.Context(), material, channels))
	require.NoError(t, db.First(material, material.Id).Error)
	assert.Equal(t, "processing", material.Status)
	selected.status = "ACTIVE"
	require.NoError(t, service.SyncMaterial(t.Context(), material, channels))
	require.NoError(t, db.First(material, material.Id).Error)
	assert.Equal(t, "ready", material.Status)
	assert.Zero(t, other.groups)
	assert.Zero(t, other.assets)
	assert.Zero(t, other.assetLists)
	assert.Equal(t, 1, selected.assets)
	var mappings []model.MaterialBinding
	require.NoError(t, db.Where("material_id = ?", material.Id).Find(&mappings).Error)
	require.Len(t, mappings, 1)
	assert.Equal(t, "channel-b", mappings[0].Scope)
	assert.Equal(t, 10, mappings[0].ChannelID)
	var unchanged model.MaterialBinding
	require.NoError(t, db.First(&unchanged, originalBinding.Id).Error)
	assert.Equal(t, originalBinding, unchanged)
	t.Run("missing selected configuration does not switch suppliers", func(t *testing.T) {
		require.Error(t, service.SyncMaterial(t.Context(), material, channels[:1]))
		require.NoError(t, db.First(material, material.Id).Error)
		assert.Equal(t, "failed", material.Status)
		assert.Contains(t, material.FailReason, "configuration")
		assert.Zero(t, other.groups)
		assert.Zero(t, other.assets)
	})
}

func TestVideoMaterialBackgroundWorkerScopes(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	require.NoError(t, db.Model(&model.Material{}).Where("status IN ?", []string{"pending", "processing", "failed"}).Update("status", "ready").Error)
	require.NoError(t, db.Model(&model.MaterialGroup{}).Where("status IN ?", []string{"pending", "processing", "failed"}).Update("status", "ready").Error)
	mobileCalls := 0
	topenCalls := make(map[string]int)
	groupNames := map[string]string{"selected-group": "selected channel group", "rename-group": "previous name"}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/api/assets/" {
			mobileCalls++
			_, _ = w.Write([]byte(`{"state":"OK","body":{"groupId":"other-group","groupType":"AIGC"}}`))
			return
		}
		action := r.URL.Query().Get("Action")
		topenCalls[action]++
		switch action {
		case "CreateAssetGroup":
			var input map[string]any
			if assert.NoError(t, common.DecodeJson(r.Body, &input)) {
				assert.NotContains(t, input, "Description")
			}
			_, _ = w.Write([]byte(`{"Result":{"Id":"selected-group","GroupType":"AIGC"}}`))
		case "CreateAsset":
			_, _ = w.Write([]byte(`{"Result":{"Id":"selected-asset","Status":"Active"}}`))
		case "GetAsset":
			_, _ = w.Write([]byte(`{"Result":{"Id":"selected-asset","GroupId":"selected-group","AssetType":"Image","Status":"Active"}}`))
		case "GetAssetGroup":
			var input map[string]any
			if !assert.NoError(t, common.DecodeJson(r.Body, &input)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprintf(w, `{"Result":{"Id":%q,"Name":%q,"GroupType":"AIGC"}}`, input["Id"], groupNames[input["Id"].(string)])
		case "UpdateAssetGroup":
			var input map[string]any
			if !assert.NoError(t, common.DecodeJson(r.Body, &input)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			groupNames[input["Id"].(string)] = input["Name"].(string)
			_, _ = w.Write([]byte(`{"Result":{}}`))
		default:
			assert.Fail(t, "unexpected material action", "%s", action)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	previousTransport := http.DefaultTransport
	transport := previousTransport.(*http.Transport).Clone()
	transport.DialTLSContext = (&tls.Dialer{Config: &tls.Config{RootCAs: server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}}).DialContext
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	channels := []model.Channel{{Name: "other account", Key: "other-key", Status: 1}, {Name: "selected account", Key: "selected-key", Status: 1}}
	require.NoError(t, db.Create(&channels).Error)
	require.NoError(t, service.SaveMobileCloudMaterialConfiguration(service.MobileCloudMaterialConfiguration{ChannelID: channels[0].Id, Endpoint: server.URL, AccessKey: "test-ak", SecretKey: "test-sk", PoolID: "test-pool"}))
	require.NoError(t, service.SaveTopenRouterMaterialConfiguration(service.TopenRouterMaterialConfiguration{ChannelID: channels[1].Id, Endpoint: server.URL, Token: "test-token"}))
	config, err := service.LoadTopenRouterMaterialConfiguration()
	require.NoError(t, err)
	for i := range 100 {
		waiting := model.MaterialGroup{UserId: users[1].Id, Name: fmt.Sprintf("unbound group %d", i)}
		require.NoError(t, model.SaveMaterialGroup(&waiting))
	}
	queuedGroup := model.MaterialGroup{UserId: users[1].Id, Name: "queued rename"}
	require.NoError(t, model.SaveMaterialGroup(&queuedGroup))
	require.NoError(t, db.Create(&model.MaterialGroupBinding{GroupId: queuedGroup.Id, UserId: users[1].Id, Scope: service.TopenRouterMaterialScope(config), ChannelID: channels[1].Id, UpstreamID: "rename-group", Name: "previous name", State: "ready"}).Error)
	unboundGroup := model.MaterialGroup{UserId: users[1].Id, Name: "waiting for model channel"}
	require.NoError(t, model.SaveMaterialGroup(&unboundGroup))
	unbound := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &unboundGroup.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?unbound", Status: "pending"}
	require.NoError(t, db.Create(&unbound).Error)
	group := model.MaterialGroup{UserId: users[1].Id, Name: "selected channel group"}
	require.NoError(t, model.SaveMaterialGroup(&group))
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &group.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&material).Error)
	binding := model.MaterialBinding{MaterialId: material.Id, Scope: service.TopenRouterMaterialScope(config), Version: model.MaterialSourceVersion(&material), ChannelID: channels[1].Id, State: "pending"}
	require.NoError(t, db.Create(&binding).Error)
	assert.True(t, (service.MaterialPreparationHandler{}).Enabled())
	require.NoError(t, service.RunMaterialPreparationOnce(t.Context()))
	assert.Zero(t, mobileCalls, "group and image preparation must not reach an unrelated channel")
	assert.Equal(t, map[string]int{"CreateAssetGroup": 1, "CreateAsset": 1, "GetAsset": 1, "UpdateAssetGroup": 1, "GetAssetGroup": 1}, topenCalls)
	require.NoError(t, db.First(&queuedGroup, queuedGroup.Id).Error)
	assert.Equal(t, "ready", queuedGroup.Status, "unbound groups must not starve an actionable rename")
	require.NoError(t, db.First(&material, material.Id).Error)
	assert.Equal(t, "ready", material.Status)
	var mappings []model.MaterialGroupBinding
	require.NoError(t, db.Where("group_id IN ?", []int{group.Id, unboundGroup.Id}).Find(&mappings).Error)
	require.Len(t, mappings, 1)
	assert.Equal(t, group.Id, mappings[0].GroupId)
	assert.Equal(t, channels[1].Id, mappings[0].ChannelID)
	t.Run("unrelated configuration errors do not overwrite selected results", func(t *testing.T) {
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MobileCloudMaterialConfigOption).Update("value", "invalid-unrelated-config").Error)
		require.NoError(t, db.Model(&material).Update("status", "pending").Error)
		require.NoError(t, db.Model(&group).Updates(map[string]any{"name": "renamed selected group", "status": "pending"}).Error)
		require.Error(t, service.RunMaterialPreparationOnce(t.Context()))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "ready", material.Status)
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "ready", group.Status)
		assert.Zero(t, mobileCalls)
		assert.Equal(t, 1, topenCalls["CreateAsset"])
		assert.Equal(t, 2, topenCalls["UpdateAssetGroup"])
	})
}

func TestVideoMaterialRecoveryStaysInSelectedChannel(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	original := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"}
	require.NoError(t, db.Create(&original).Error)
	otherBinding := model.MaterialBinding{MaterialId: original.Id, ChannelID: 5, Scope: "channel-a", Version: model.MaterialSourceVersion(&original), Name: "other-name", GroupID: "other-group", AssetID: "other-ready-asset", State: "ready", UpstreamStatus: "ACTIVE"}
	require.NoError(t, db.Create(&otherBinding).Error)
	current := original
	current.Id, current.MaterialID, current.Status = 0, model.NewPlatformMaterialID(), "failed"
	require.NoError(t, db.Create(&current).Error)
	binding := model.MaterialBinding{MaterialId: current.Id, ChannelID: 10, Scope: "channel-b", Version: model.MaterialSourceVersion(&current), Name: "current-name", GroupID: "upstream-group", State: "uncertain", StartedAt: common.GetTimestamp()}
	require.NoError(t, db.Create(&binding).Error)
	provider := &materialPreparationProvider{status: "ACTIVE", assetPages: [][]service.MobileCloudAsset{{{ID: "upstream-asset", GroupID: binding.GroupID, Name: binding.Name, Type: "Image"}}}}
	channel := service.MaterialChannel{ChannelID: binding.ChannelID, Scope: binding.Scope, Client: provider}
	resolved, issues, err := service.PrepareVideoMaterials(t.Context(), users[1].Id, channel, []service.MaterialImageInput{{Source: publicMaterialURL}})
	require.NoError(t, err)
	require.Empty(t, issues)
	assert.Equal(t, []string{"asset://upstream-asset"}, resolved)
	assert.Equal(t, 1, provider.assetLists)
	assert.Equal(t, []string{binding.GroupID}, provider.assetQuery.GroupIDs)
	require.NotNil(t, provider.assetQuery.Name)
	assert.Equal(t, binding.Name, *provider.assetQuery.Name)
	assert.Zero(t, provider.groups)
	assert.Zero(t, provider.assets, "unknown uploads must be recovered rather than recreated or replaced with another channel's ready asset")
	require.NoError(t, db.First(&binding, binding.Id).Error)
	assert.Equal(t, "upstream-asset", binding.AssetID)
	assert.Equal(t, "ready", binding.State)
	var unchanged model.MaterialBinding
	require.NoError(t, db.First(&unchanged, otherBinding.Id).Error)
	assert.Equal(t, otherBinding, unchanged)
	_, err = model.ReserveVideoMaterials(db, users[1].Id, channel.Scope, "current-video", []string{"upstream-asset"})
	require.NoError(t, err)
	var reserved []model.MaterialUseLease
	require.NoError(t, db.Where("owner = ?", "current-video").Find(&reserved).Error)
	require.Len(t, reserved, 1)
	assert.Equal(t, binding.Id, reserved[0].BindingId)
}

func TestVideoMaterialRecoversHistoricalChannelBinding(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, tc := range []struct {
		name, status                          string
		deleting, groupDeleting, foreignLease bool
	}{
		{name: "failed", status: "failed"},
		{name: "pending", status: "pending"},
		{name: "other channel material deletion", status: "processing", deleting: true},
		{name: "other channel group deletion", status: "processing", deleting: true, groupDeleting: true},
		{name: "other channel deletion lease", status: "processing", deleting: true, foreignLease: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := model.MaterialGroup{UserId: users[1].Id, Name: "original " + tc.name, Status: "ready", Deleting: tc.groupDeleting}
			require.NoError(t, group.Validate())
			require.NoError(t, db.Create(&group).Error)
			original := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &group.Id, Name: "historical image", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?historical=" + url.QueryEscape(tc.name), Status: tc.status, Deleting: tc.deleting}
			require.NoError(t, db.Create(&original).Error)
			foreign := model.MaterialBinding{MaterialId: original.Id, Scope: "original-" + tc.name, Version: model.MaterialSourceVersion(&original), ChannelID: 5, Provider: "mobile_cloud", GroupID: "original-group", AssetID: "original-asset", State: "ready", UpstreamStatus: "ACTIVE"}
			if tc.foreignLease {
				foreign.State, foreign.LockedBy, foreign.LockedUntil = "deleting", "original-deletion-worker", common.GetTimestamp()+120
			}
			require.NoError(t, db.Create(&foreign).Error)
			groupBinding := model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: foreign.Scope, ChannelID: foreign.ChannelID, UpstreamID: foreign.GroupID, Name: group.Name, State: "ready"}
			require.NoError(t, db.Create(&groupBinding).Error)
			failed := model.MaterialBinding{MaterialId: original.Id, Scope: "selected-" + tc.name, Version: foreign.Version, ChannelID: 10, Provider: "topenrouter", Name: "historical-name", State: "failed", FailReason: "Material group is not ready in this channel; synchronize the group and retry", CanRetry: true, StartedAt: common.GetTimestamp()}
			require.NoError(t, db.Create(&failed).Error)
			provider := &materialPreparationProvider{status: "ACTIVE"}
			channel := service.MaterialChannel{ChannelID: failed.ChannelID, Provider: failed.Provider, Scope: failed.Scope, Client: provider}
			inputs := []service.MaterialImageInput{{Index: 1, Source: original.SourceURL}, {Index: 2, Source: original.SourceURL}}
			if tc.name == "failed" || tc.name == "other channel material deletion" {
				t.Run("creation failure rolls back recovery", func(t *testing.T) {
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("material_test_recovery_create_failure", func(tx *gorm.DB) {
						if tx.Statement.Table == "materials" {
							tx.AddError(errors.New("material fixture creation failed"))
						}
					}))
					t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove("material_test_recovery_create_failure")) })
					_, err := model.FindOrCreateVideoMaterial(db, original.UserId, original.SourceURL, channel.Scope)
					require.Error(t, err)
					var unchanged model.MaterialBinding
					require.NoError(t, db.First(&unchanged, failed.Id).Error)
					assert.Equal(t, failed, unchanged)
					var stored model.Material
					require.NoError(t, db.First(&stored, original.Id).Error)
					assert.Equal(t, original, stored)
				})
			}
			for range 2 {
				resolved, issues, err := service.PrepareVideoMaterials(t.Context(), users[1].Id, channel, inputs)
				require.NoError(t, err)
				require.Empty(t, issues)
				assert.Equal(t, []string{"asset://upstream-asset", "asset://upstream-asset"}, resolved)
			}
			assert.Equal(t, 1, provider.assets)
			assert.Equal(t, 1, provider.groups)
			var records []model.Material
			require.NoError(t, db.Where("user_id = ? AND source_url = ?", original.UserId, original.SourceURL).Order("id").Find(&records).Error)
			require.Len(t, records, 2)
			expectedOriginal := original
			if !tc.deleting {
				expectedOriginal.Status, expectedOriginal.UpdatedTime = "ready", records[0].UpdatedTime
			}
			assert.Equal(t, expectedOriginal, records[0], "recovery must preserve the original channel's deletion and resource ownership")
			assert.Nil(t, records[1].GroupId)
			assert.False(t, records[1].Deleting)
			var ready model.MaterialBinding
			require.NoError(t, db.Where("material_id = ? AND scope = ?", records[1].Id, channel.Scope).First(&ready).Error)
			assert.Equal(t, channel.ChannelID, ready.ChannelID)
			assert.Equal(t, "ready", ready.State)
			assert.Equal(t, "ACTIVE", ready.UpstreamStatus)
			require.NoError(t, service.PrepareMaterial(t.Context(), &records[0], channel.Scope, provider))
			assert.Equal(t, 1, provider.assets, "a stale worker must not reactivate the superseded binding")
			failed.State = "deleted"
			for _, binding := range []model.MaterialBinding{foreign, failed} {
				var unchanged model.MaterialBinding
				require.NoError(t, db.First(&unchanged, binding.Id).Error)
				assert.Equal(t, binding, unchanged)
			}
			var unchanged model.MaterialGroupBinding
			require.NoError(t, db.First(&unchanged, groupBinding.Id).Error)
			assert.Equal(t, groupBinding, unchanged)
			var storedGroup model.MaterialGroup
			require.NoError(t, db.First(&storedGroup, group.Id).Error)
			assert.Equal(t, group, storedGroup)
			if tc.deleting {
				_, err := model.ReserveVideoMaterials(db, original.UserId, channel.Scope, "selected-video", []string{ready.AssetID})
				require.NoError(t, err)
				deleting := &materialPreparationProvider{}
				require.NoError(t, service.SyncMaterial(t.Context(), &records[0], []service.MaterialChannel{{ChannelID: foreign.ChannelID, Scope: foreign.Scope, Client: deleting}}))
				if tc.foreignLease {
					assert.Zero(t, deleting.assetDeletes)
				} else {
					assert.Equal(t, 1, deleting.assetDeletes, "original deletion must continue independently")
				}
				require.NoError(t, db.First(&ready, ready.Id).Error)
				assert.Equal(t, "ready", ready.State)
				assert.Empty(t, ready.LockedBy)
				assert.Zero(t, ready.LockedUntil)
				var lease model.MaterialUseLease
				require.NoError(t, db.Where("owner = ? AND binding_id = ?", "selected-video", ready.Id).First(&lease).Error)
				assert.GreaterOrEqual(t, lease.ExpiresAt, common.GetTimestamp())
				assert.Equal(t, 1, provider.assets)
				assert.Zero(t, provider.assetDeletes)
			}
		})
	}
}

func TestVideoMaterialHistoricalRecoveryPreservesUploadOwnership(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, tc := range []struct {
		name, state, groupID, assetID string
		locked, deleting, targetGroup bool
	}{
		{name: "unknown upload", state: "uncertain", groupID: "upstream-group"},
		{name: "creation in progress", state: "creating"},
		{name: "processing asset", state: "processing", groupID: "upstream-group", assetID: "upstream-asset"},
		{name: "failed with upload history", state: "failed", groupID: "upstream-group"},
		{name: "failed with asset", state: "failed", assetID: "upstream-asset"},
		{name: "active preparation lease", state: "failed", locked: true},
		{name: "target asset deletion", state: "deleting", groupID: "upstream-group", assetID: "upstream-asset", deleting: true},
		{name: "unknown target upload during deletion", state: "uncertain", groupID: "upstream-group", deleting: true},
		{name: "active target deletion lease", state: "failed", locked: true, deleting: true},
		{name: "target group deletion", state: "failed", deleting: true, targetGroup: true},
		{name: "group already bound to target", state: "failed", targetGroup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := model.MaterialGroup{UserId: users[1].Id, Name: tc.name, Status: "ready"}
			require.NoError(t, group.Validate())
			require.NoError(t, db.Create(&group).Error)
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &group.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?guard=" + url.QueryEscape(tc.name), Status: "failed", Deleting: tc.deleting}
			require.NoError(t, db.Create(&material).Error)
			foreign := model.MaterialBinding{MaterialId: material.Id, Scope: "original", Version: model.MaterialSourceVersion(&material), GroupID: "original-group", AssetID: "original-asset", State: "ready", UpstreamStatus: "ACTIVE"}
			require.NoError(t, db.Create(&foreign).Error)
			require.NoError(t, db.Create(&model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: foreign.Scope, UpstreamID: foreign.GroupID, Name: group.Name, State: "ready"}).Error)
			binding := model.MaterialBinding{MaterialId: material.Id, Scope: "selected", Version: foreign.Version, Name: "stable-name", GroupID: tc.groupID, AssetID: tc.assetID, State: tc.state, StartedAt: common.GetTimestamp()}
			if tc.locked {
				binding.LockedBy, binding.LockedUntil = "existing-worker", common.GetTimestamp()+120
			}
			require.NoError(t, db.Create(&binding).Error)
			if tc.targetGroup {
				require.NoError(t, db.Create(&model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: binding.Scope, UpstreamID: "upstream-group", Name: group.Name, State: "ready"}).Error)
			}
			found, err := model.FindOrCreateVideoMaterial(db, material.UserId, material.SourceURL, binding.Scope)
			require.NoError(t, err)
			assert.Equal(t, material.Id, found.Id, "existing upload ownership or target group must not be bypassed")
			var unchanged model.MaterialBinding
			require.NoError(t, db.First(&unchanged, binding.Id).Error)
			assert.Equal(t, binding, unchanged)
			var count int64
			require.NoError(t, db.Model(&model.Material{}).Where("user_id = ? AND source_url = ?", material.UserId, material.SourceURL).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestVideoMaterialIgnoresUnboundLibraryGroup(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	group := model.MaterialGroup{UserId: users[1].Id, Name: "unbound library group"}
	require.NoError(t, db.Create(&group).Error)
	original := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &group.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&original).Error)
	provider := &materialPreparationProvider{status: "ACTIVE"}
	resolved, issues, err := service.PrepareVideoMaterials(t.Context(), users[1].Id, service.MaterialChannel{ChannelID: 10, Scope: "channel-b", Client: provider}, []service.MaterialImageInput{{Source: publicMaterialURL}})
	require.NoError(t, err)
	require.Empty(t, issues)
	assert.Equal(t, []string{"asset://upstream-asset"}, resolved)
	assert.Equal(t, 1, provider.assets)
	var count int64
	require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", original.Id).Count(&count).Error)
	assert.Zero(t, count, "a URL request must not enroll an unrelated local record into its channel")
}

func TestVideoMaterialSourceComparisonIsExact(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.Exec("CREATE COLLATION material_test_case_insensitive (provider = icu, locale = 'und-u-ks-level2', deterministic = false)").Error)
	require.NoError(t, db.Exec("ALTER TABLE materials ALTER COLUMN source_url TYPE text COLLATE material_test_case_insensitive").Error)
	original := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "case-sensitive URL", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?image=Frame.png", Status: "ready"}
	require.NoError(t, db.Create(&original).Error)
	require.NoError(t, db.Create(&model.MaterialBinding{MaterialId: original.Id, Scope: "exact-source", Version: model.MaterialSourceVersion(&original), AssetID: "uppercase-asset", State: "ready", UpstreamStatus: "ACTIVE"}).Error)
	requested := publicMaterialURL + "?image=frame.png"
	found, err := model.FindOrCreateVideoMaterial(db, users[1].Id, requested, "exact-source")
	require.NoError(t, err)
	assert.NotEqual(t, original.Id, found.Id, "case-insensitive SQL equality must not substitute a different image")
	assert.Equal(t, requested, found.SourceURL)
	reused, err := model.FindOrCreateVideoMaterial(db, users[1].Id, original.SourceURL, "exact-source")
	require.NoError(t, err)
	assert.Equal(t, original.Id, reused.Id)
}

func TestVideoMaterialReusesExistingLibraryRecord(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "admin image", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"}
	require.NoError(t, db.Create(&material).Error)
	binding := model.MaterialBinding{MaterialId: material.Id, Scope: "video-scope", Version: model.MaterialSourceVersion(&material), AssetID: "existing-asset", GroupID: "existing-group", State: "ready", UpstreamStatus: "ACTIVE"}
	require.NoError(t, db.Create(&binding).Error)
	duplicate := material
	duplicate.Id, duplicate.MaterialID = 0, model.NewPlatformMaterialID()
	require.NoError(t, db.Create(&duplicate).Error)
	duplicateBinding := binding
	duplicateBinding.Id, duplicateBinding.MaterialId, duplicateBinding.AssetID = 0, duplicate.Id, "historical-duplicate-asset"
	require.NoError(t, db.Create(&duplicateBinding).Error)
	provider := &materialPreparationProvider{status: "ACTIVE"}
	channel := service.MaterialChannel{ChannelID: 5, Scope: binding.Scope, Client: provider}
	resolved, issues, err := service.PrepareVideoMaterials(context.Background(), users[1].Id, channel, []service.MaterialImageInput{{Source: publicMaterialURL}})
	require.NoError(t, err)
	require.Empty(t, issues)
	assert.Equal(t, []string{"asset://existing-asset"}, resolved)
	assert.Zero(t, provider.assets)
	assert.Zero(t, provider.groups)
}

func TestVideoMaterialReusesInFlightDuplicate(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, state := range []string{"processing", "uncertain"} {
		t.Run(state, func(t *testing.T) {
			source := publicMaterialURL + "?state=" + state
			first := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "unbound duplicate", LibraryType: "AIGC", FileType: "Image", SourceURL: source, Status: "pending"}
			require.NoError(t, db.Create(&first).Error)
			uploaded := first
			uploaded.Id, uploaded.MaterialID, uploaded.Status = 0, model.NewPlatformMaterialID(), "processing"
			require.NoError(t, db.Create(&uploaded).Error)
			binding := model.MaterialBinding{MaterialId: uploaded.Id, Scope: "video-scope", Version: model.MaterialSourceVersion(&uploaded), Name: "uploaded-image", GroupID: "upstream-group", State: state, StartedAt: common.GetTimestamp()}
			if state == "processing" {
				binding.AssetID = "upstream-asset"
			}
			require.NoError(t, db.Create(&binding).Error)
			provider := &materialPreparationProvider{status: "ACTIVE", assetPages: [][]service.MobileCloudAsset{{{ID: "upstream-asset", GroupID: binding.GroupID, Name: binding.Name, Type: "Image"}}}}
			channel := service.MaterialChannel{ChannelID: 5, Scope: binding.Scope, Client: provider}
			resolved, issues, err := service.PrepareVideoMaterials(context.Background(), users[1].Id, channel, []service.MaterialImageInput{{Source: source}})
			require.NoError(t, err)
			require.Empty(t, issues)
			assert.Equal(t, []string{"asset://upstream-asset"}, resolved)
			assert.Zero(t, provider.assets, "an earlier unbound duplicate must not cause another upload")
			assert.Zero(t, provider.groups)
			if state == "uncertain" {
				assert.Equal(t, 1, provider.assetLists, "recover the unknown upload before considering a new upload")
			}
		})
	}
}

func TestVideoMaterialPreflightDoesNotQueueInvalidRequest(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	provider := &materialPreparationProvider{status: "ACTIVE"}
	channel := service.MaterialChannel{ChannelID: 5, Scope: "video-scope", Client: provider}
	for _, invalid := range []string{"http://localhost/image.png", "asset://foreign-asset"} {
		inputs := []service.MaterialImageInput{{Index: 0, Source: publicMaterialURL}, {Index: 2, Source: invalid, Field: "content[2].image_url.url", Usage: "last_frame"}}
		resolved, issues, err := service.PrepareVideoMaterials(context.Background(), users[1].Id, channel, inputs)
		require.NoError(t, err)
		assert.Nil(t, resolved)
		require.Len(t, issues, 1)
		assert.Equal(t, 2, issues[0].Index)
		var count int64
		require.NoError(t, db.Model(&model.Material{}).Where("user_id = ?", users[1].Id).Count(&count).Error)
		assert.Zero(t, count, "invalid requests must not queue background uploads")
		assert.Zero(t, provider.assets)
	}
}

func TestVideoMaterialRejectsSourceChangedDuringPreparation(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	provider := &materialPreparationProvider{status: "ACTIVE"}
	provider.onAssetGet = func() {
		var material model.Material
		require.NoError(t, db.Where("user_id = ? AND source_url = ?", users[1].Id, publicMaterialURL).First(&material).Error)
		require.NoError(t, db.Model(&material).Update("source_url", publicMaterialURL+"?changed").Error)
		binding := model.MaterialBinding{MaterialId: material.Id, Scope: "video-scope", Version: model.MaterialSourceVersion(&material), AssetID: "different-asset", State: "ready", UpstreamStatus: "ACTIVE"}
		require.NoError(t, db.Create(&binding).Error)
	}
	channel := service.MaterialChannel{ChannelID: 5, Scope: "video-scope", Client: provider}
	resolved, issues, err := service.PrepareVideoMaterials(context.Background(), users[1].Id, channel, []service.MaterialImageInput{{Index: 3, Source: publicMaterialURL, Field: "content[3].image_url.url", Usage: "reference_image"}})
	require.NoError(t, err)
	assert.Nil(t, resolved)
	require.Len(t, issues, 1)
	assert.Equal(t, "unavailable", issues[0].Status)
	assert.Equal(t, 3, issues[0].Index)
}

func TestVideoMaterialDuplicateUploadClaim(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	first := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "selected before competing upload", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&first).Error)
	second := first
	second.Id, second.MaterialID = 0, model.NewPlatformMaterialID()
	require.NoError(t, db.Create(&second).Error)
	binding, claimed, err := model.ClaimMaterialBinding(db, &second, "duplicate-scope", "competing-worker")
	require.NoError(t, err)
	require.True(t, claimed)
	binding.State = "creating"
	require.NoError(t, model.SaveMaterialBinding(db, binding, "competing-worker"))
	provider := &materialPreparationProvider{status: "ACTIVE"}
	assert.Error(t, service.PrepareMaterial(t.Context(), &first, "duplicate-scope", provider))
	assert.Zero(t, provider.assets, "a competing library row must not upload the same source")
	assert.Zero(t, provider.groups)
}

func TestVideoMaterialRevalidatesEarlierImages(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	first := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "first frame", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?first", Status: "ready"}
	require.NoError(t, db.Create(&first).Error)
	binding := model.MaterialBinding{MaterialId: first.Id, Scope: "video-scope", Version: model.MaterialSourceVersion(&first), AssetID: "first-asset", State: "ready", UpstreamStatus: "ACTIVE"}
	require.NoError(t, db.Create(&binding).Error)
	provider := &materialPreparationProvider{status: "ACTIVE", onAssetGet: func() {
		require.NoError(t, db.Model(&first).Update("deleting", true).Error)
	}}
	inputs := []service.MaterialImageInput{{Index: 1, Source: first.SourceURL, Field: "content[1].image_url.url", Usage: "first_frame"}, {Index: 3, Source: publicMaterialURL, Field: "content[3].image_url.url", Usage: "last_frame"}}
	resolved, issues, err := service.PrepareVideoMaterials(t.Context(), users[1].Id, service.MaterialChannel{Scope: binding.Scope, Client: provider}, inputs)
	require.NoError(t, err)
	assert.Nil(t, resolved)
	require.Len(t, issues, 1)
	assert.Equal(t, first.MaterialID, issues[0].MaterialID)
	assert.Equal(t, 1, issues[0].Index)
	assert.Equal(t, "unavailable", issues[0].Status)
}

func TestVideoMaterialPreparationBlocksPartialReferences(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	provider := &materialPreparationProvider{status: "PROCESSING"}
	channel := service.MaterialChannel{ChannelID: 5, Scope: "video-scope", Client: provider}
	inputs := []service.MaterialImageInput{{Index: 4, Source: publicMaterialURL, Field: "content[4].image_url.url", Usage: "last_frame"}}
	ctx, cancel := context.WithCancel(context.Background())
	provider.onAssetGet = cancel
	resolved, issues, err := service.PrepareVideoMaterials(ctx, users[1].Id, channel, inputs)
	require.NoError(t, err)
	assert.Nil(t, resolved)
	require.Len(t, issues, 1)
	assert.Equal(t, 4, issues[0].Index)
	assert.Equal(t, inputs[0].Field, issues[0].Field)
	assert.Equal(t, inputs[0].Usage, issues[0].Usage)
	assert.NotEmpty(t, issues[0].MaterialID)
	assert.Equal(t, 1, provider.assets)

	provider.onAssetGet = nil
	provider.status = "ACTIVE"
	resolved, issues, err = service.PrepareVideoMaterials(context.Background(), users[1].Id, channel, inputs)
	require.NoError(t, err)
	require.Empty(t, issues)
	assert.Equal(t, []string{"asset://upstream-asset"}, resolved)
	assert.Equal(t, 1, provider.assets, "an interrupted wait must not upload again")

	var material model.Material
	require.NoError(t, db.Where("user_id = ? AND source_url = ?", users[1].Id, publicMaterialURL).First(&material).Error)
	require.NoError(t, db.Model(&material).Update("deleting", true).Error)
	resolved, issues, err = service.PrepareVideoMaterials(context.Background(), users[1].Id, channel, inputs)
	require.NoError(t, err)
	assert.Nil(t, resolved)
	require.Len(t, issues, 1)
	assert.Equal(t, "unavailable", issues[0].Status)
	assert.Equal(t, 1, provider.assets)
}

func TestAIGCMaterialLeaseRecovery(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, model.MigrateMaterialPreparation(db))
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "leased image", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&material).Error)
	binding, claimed, err := model.ClaimMaterialBinding(db, &material, "leased-scope", "previous-worker")
	require.NoError(t, err)
	require.True(t, claimed)
	provider := &materialPreparationProvider{status: "ACTIVE"}
	require.NoError(t, service.PrepareMaterial(context.Background(), &material, "leased-scope", provider))
	assert.Zero(t, provider.groups)
	assert.Zero(t, provider.assets, "an active lease must prevent another worker from uploading")
	require.NoError(t, db.Model(binding).Update("locked_until", common.GetTimestamp()-1).Error)
	require.NoError(t, service.PrepareMaterial(context.Background(), &material, "leased-scope", provider))
	require.NoError(t, db.First(&material, material.Id).Error)
	assert.Equal(t, model.MaterialStatusReady, material.Status)
	assert.Equal(t, 1, provider.groups)
	assert.Equal(t, 1, provider.assets)
	binding.State = "failed"
	assert.ErrorContains(t, model.SaveMaterialBinding(db, binding, "previous-worker"), "lease lost")
	var stored model.MaterialBinding
	require.NoError(t, db.First(&stored, binding.Id).Error)
	assert.Equal(t, "ready", stored.State, "the old worker must not overwrite the recovered result")
}

func TestAIGCMaterialRetryRecovery(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, model.MigrateMaterialPreparation(db))
	for _, tc := range []struct {
		name        string
		createErr   error
		status      string
		wantUploads int
	}{
		{"uncertain upload only queries", context.DeadlineExceeded, "ACTIVE", 1},
		{"explicit rejection permits retry", &service.MobileCloudMaterialRejection{Message: "Unsupported image format https://private.example/image?token=private-token"}, "ACTIVE", 2},
		{"processing failure permits retry", nil, "FAILED", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: tc.name, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
			require.NoError(t, db.Create(&material).Error)
			provider := &materialPreparationProvider{status: tc.status, createErr: tc.createErr, omitLookup: errors.Is(tc.createErr, context.DeadlineExceeded), reason: "Unsupported image format https://private.example/image?token=private-token"}
			scope := material.MaterialID
			require.NoError(t, service.PrepareMaterial(context.Background(), &material, scope, provider))
			require.NoError(t, db.First(&material, material.Id).Error)
			require.Equal(t, "failed", material.Status)
			assert.True(t, material.CanRetry)
			assert.Contains(t, material.FailReason, material.MaterialID)
			assert.NotContains(t, material.FailReason, "private-token")
			assert.NotContains(t, material.FailReason, "private.example")
			if tc.status == "FAILED" || tc.createErr != nil && tc.wantUploads == 2 {
				assert.Contains(t, material.FailReason, "Unsupported image format")
				require.NoError(t, db.Model(&material).Updates(map[string]any{"status": "processing", "fail_reason": "", "can_retry": false}).Error)
				require.NoError(t, service.PrepareMaterial(context.Background(), &material, scope, provider))
				require.NoError(t, db.First(&material, material.Id).Error)
				assert.Equal(t, model.MaterialStatusFailed, material.Status, "a persisted binding failure must restore the user-visible result after interruption")
				assert.True(t, material.CanRetry)
				assert.Contains(t, material.FailReason, "Unsupported image format")
				assert.Equal(t, 1, provider.assets, "recovering the failed result must not upload again")
			}
			_, err := model.RetryUserMaterial(users[2].Id, material.Id)
			require.Error(t, err)
			retried, err := model.RetryUserMaterial(users[1].Id, material.Id)
			require.NoError(t, err)
			provider.createErr, provider.status, provider.omitLookup = nil, "ACTIVE", false
			require.NoError(t, service.PrepareMaterial(context.Background(), retried, scope, provider))
			require.NoError(t, db.First(&material, material.Id).Error)
			assert.Equal(t, "ready", material.Status)
			assert.Equal(t, tc.wantUploads, provider.assets)
			assert.Equal(t, 1, provider.groups)
			if tc.status == "FAILED" {
				var bindings []model.MaterialBinding
				require.NoError(t, db.Where("material_id = ?", material.Id).Find(&bindings).Error)
				assert.Len(t, bindings, 2, "retry must retain the failed remote asset for eventual deletion")
			}
		})
	}
}

func TestAIGCMaterialResponseMismatchRecheck(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, tc := range []struct {
		name       string
		assetID    string
		groupID    string
		assetType  string
		wantFields string
		nextStatus string
		stillWrong bool
		wantState  string
		queryError bool
	}{
		{"asset ID differs then recovers", "foreign-asset", "upstream-group", "Image", "asset ID", "ACTIVE", false, "ready", false},
		{"missing group then recovers", "upstream-asset", "", "Image", "group ID", "ACTIVE", false, "ready", false},
		{"different group stays unavailable", "upstream-asset", "foreign-group", "Image", "group ID", "ACTIVE", true, "uncertain", false},
		{"missing type resumes processing", "upstream-asset", "upstream-group", "", "asset type", "PROCESSING", false, "processing", false},
		{"wrong type stays unavailable", "upstream-asset", "upstream-group", "Video", "asset type", "ACTIVE", true, "uncertain", false},
		{"multiple differences are identified", "https://private.example/asset?token=private-token", "foreign-group", "Video", "asset ID, group ID, asset type", "ACTIVE", false, "ready", false},
		{"query error preserves the existing upload", "upstream-asset", "", "Image", "group ID", "ACTIVE", false, "ready", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: tc.name, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "processing"}
			require.NoError(t, db.Create(&material).Error)
			binding := model.MaterialBinding{MaterialId: material.Id, Scope: material.MaterialID, Version: model.MaterialSourceVersion(&material), ChannelID: 16, Provider: "topenrouter", GroupID: "upstream-group", AssetID: "upstream-asset", State: "processing", StartedAt: common.GetTimestamp() - 601}
			require.NoError(t, db.Create(&binding).Error)
			provider := &materialPreparationProvider{assetResponse: &service.MobileCloudAsset{ID: tc.assetID, GroupID: tc.groupID, Type: tc.assetType, Status: "ACTIVE"}}

			require.NoError(t, service.PrepareMaterial(t.Context(), &material, binding.Scope, provider))
			require.NoError(t, db.First(&material, material.Id).Error)
			require.NoError(t, db.First(&binding, binding.Id).Error)
			assert.Equal(t, "failed", material.Status)
			assert.True(t, material.CanRetry)
			assert.Equal(t, "uncertain", binding.State)
			assert.True(t, binding.CanRetry)
			assert.Equal(t, "Material response does not match the requested material: "+tc.wantFields, binding.FailReason)
			assert.NotContains(t, material.FailReason, "private.example")
			assert.NotContains(t, material.FailReason, "private-token")
			resolved, issues, err := service.ResolvePreparedMaterials(t.Context(), material.UserId, binding.Scope, []service.MaterialReference{{ID: material.Id}})
			require.NoError(t, err)
			assert.Nil(t, resolved)
			require.Len(t, issues, 1)

			retried, err := model.RetryUserMaterial(material.UserId, material.Id)
			require.NoError(t, err)
			if !tc.stillWrong {
				provider.assetResponse = &service.MobileCloudAsset{ID: binding.AssetID, GroupID: binding.GroupID, Type: "Image", Status: tc.nextStatus}
			}
			if tc.queryError {
				provider.getErr = context.DeadlineExceeded
				require.NoError(t, service.PrepareMaterial(t.Context(), retried, binding.Scope, provider))
				require.NoError(t, db.First(&material, material.Id).Error)
				require.NoError(t, db.First(&binding, binding.Id).Error)
				assert.Equal(t, "processing", material.Status)
				assert.Equal(t, "upstream-asset", binding.AssetID)
				assert.Equal(t, "upstream-group", binding.GroupID)
				assert.Zero(t, provider.assets)
				provider.getErr = nil
			}
			require.NoError(t, service.PrepareMaterial(t.Context(), retried, binding.Scope, provider))
			require.NoError(t, db.First(&material, material.Id).Error)
			require.NoError(t, db.First(&binding, binding.Id).Error)
			assert.Equal(t, tc.wantState, binding.State)
			assert.Equal(t, "upstream-asset", binding.AssetID)
			assert.Equal(t, "upstream-group", binding.GroupID)
			assert.Zero(t, provider.groups)
			assert.Zero(t, provider.assets)
			assert.Zero(t, provider.groupLists)
			assert.Zero(t, provider.assetLists)
			wantGets := 2
			if tc.queryError {
				wantGets++
			}
			assert.Equal(t, wantGets, provider.assetGets)
			if tc.stillWrong {
				assert.Equal(t, "failed", material.Status)
				assert.True(t, material.CanRetry)
			} else {
				assert.Equal(t, tc.wantState, material.Status)
				assert.Empty(t, material.FailReason)
				assert.Empty(t, binding.FailReason)
				assert.Equal(t, tc.nextStatus, binding.UpstreamStatus)
			}
			if tc.wantState == "processing" {
				provider.assetResponse.Status = "ACTIVE"
				require.NoError(t, service.PrepareMaterial(t.Context(), &material, binding.Scope, provider))
				require.NoError(t, db.First(&material, material.Id).Error)
				assert.Equal(t, "ready", material.Status)
				assert.Zero(t, provider.assets)
			}
		})
	}
}

func TestAIGCMaterialResponseMismatchRecheckIsolation(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, tc := range []struct {
		name       string
		reason     string
		otherState string
		canRetry   bool
	}{
		{"legacy mismatch does not retry another failure", "Material response does not match the requested material", "failed", false},
		{"new mismatch does not upload another pending binding", "Material response does not match the requested material: group ID", "pending", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := model.MaterialGroup{UserId: users[1].Id, Name: tc.name}
			require.NoError(t, model.SaveMaterialGroup(&group))
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &group.Id, Name: tc.name, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "failed", CanRetry: tc.canRetry}
			require.NoError(t, db.Create(&material).Error)
			other := model.MaterialBinding{MaterialId: material.Id, Scope: material.MaterialID + "-other", Version: model.MaterialSourceVersion(&material), ChannelID: 5, Provider: "mobile_cloud", GroupID: "upstream-group", State: tc.otherState, CanRetry: true, FailReason: "An unrelated channel failure"}
			if tc.otherState == "failed" {
				other.AssetID = "failed-asset"
			}
			require.NoError(t, db.Create(&other).Error)
			binding := model.MaterialBinding{MaterialId: material.Id, Scope: material.MaterialID, Version: other.Version, ChannelID: 16, Provider: "topenrouter", GroupID: "upstream-group", AssetID: "upstream-asset", State: "uncertain", CanRetry: tc.canRetry, FailReason: tc.reason, StartedAt: common.GetTimestamp()}
			require.NoError(t, db.Create(&binding).Error)
			provider, otherProvider := &materialPreparationProvider{status: "ACTIVE"}, &materialPreparationProvider{status: "ACTIVE"}
			retried, err := model.RetryUserMaterial(material.UserId, material.Id)
			require.NoError(t, err)
			channels := []service.MaterialChannel{{ChannelID: 5, Scope: other.Scope, Provider: other.Provider, Client: otherProvider}, {ChannelID: 16, Scope: binding.Scope, Provider: binding.Provider, Client: provider}}

			require.NoError(t, service.SyncMaterial(t.Context(), retried, channels))

			require.NoError(t, db.First(&binding, binding.Id).Error)
			assert.Equal(t, "ready", binding.State)
			assert.Equal(t, "upstream-asset", binding.AssetID)
			require.NoError(t, db.First(&other, other.Id).Error)
			assert.Equal(t, tc.otherState, other.State)
			assert.Equal(t, "An unrelated channel failure", other.FailReason)
			if tc.otherState == "failed" {
				assert.Equal(t, "failed-asset", other.AssetID)
			} else {
				assert.Empty(t, other.AssetID)
			}
			assert.Equal(t, 1, provider.assetGets)
			assert.Zero(t, otherProvider.assetGets)
			assert.Zero(t, provider.groups)
			assert.Zero(t, provider.assets)
			assert.Zero(t, otherProvider.groups)
			assert.Zero(t, otherProvider.assets)
			assert.Zero(t, provider.updates)
			assert.Zero(t, otherProvider.updates)
		})
	}
}

func TestAIGCMaterialResponseMismatchLegacyRetry(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, tc := range []struct {
		name      string
		state     string
		assetID   string
		groupID   string
		reason    string
		stale     bool
		deleting  bool
		wantRetry bool
	}{
		{"known asset can be rechecked", "uncertain", "upstream-asset", "upstream-group", "Material response does not match the requested material", false, false, true},
		{"unknown upload cannot be retried", "uncertain", "", "upstream-group", "Material response does not match the requested material", false, false, false},
		{"unknown group cannot be retried", "uncertain", "upstream-asset", "", "Material response does not match the requested material", false, false, false},
		{"other uncertainty remains blocked", "uncertain", "upstream-asset", "upstream-group", "Multiple matching materials require administrator review", false, false, false},
		{"stale binding remains blocked", "uncertain", "upstream-asset", "upstream-group", "Material response does not match the requested material", true, false, false},
		{"failed upload remains blocked", "failed", "upstream-asset", "upstream-group", "Material response does not match the requested material", false, false, false},
		{"deleting material remains blocked", "uncertain", "upstream-asset", "upstream-group", "Material response does not match the requested material", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: tc.name, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "failed", CanRetry: false, Deleting: tc.deleting, FailReason: tc.reason}
			require.NoError(t, db.Create(&material).Error)
			binding := model.MaterialBinding{MaterialId: material.Id, Scope: material.MaterialID, Version: model.MaterialSourceVersion(&material), ChannelID: 16, Provider: "topenrouter", GroupID: tc.groupID, AssetID: tc.assetID, State: tc.state, FailReason: tc.reason, StartedAt: common.GetTimestamp()}
			if tc.stale {
				binding.Version = "previous-source"
			}
			require.NoError(t, db.Create(&binding).Error)

			recorder := httptest.NewRecorder()
			c := newMaterialContext(recorder, http.MethodGet, "/api/material/list?keyword="+material.MaterialID, "", material.UserId)
			ListMaterials(c)
			var listed materialListEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &listed))
			require.True(t, listed.Success)
			require.Len(t, listed.Data, 1)
			assert.Equal(t, tc.wantRetry, listed.Data[0].CanRetry)
			var stored model.Material
			require.NoError(t, db.First(&stored, material.Id).Error)
			assert.False(t, stored.CanRetry, "listing must not modify persisted material state")

			recorder = httptest.NewRecorder()
			c = newMaterialContext(recorder, http.MethodPost, "/api/material/"+strconv.Itoa(material.Id)+"/retry", "", users[2].Id)
			c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(material.Id)}}
			RetryMaterial(c)
			var denied materialEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &denied))
			assert.False(t, denied.Success)

			recorder = httptest.NewRecorder()
			c = newMaterialContext(recorder, http.MethodPost, "/api/material/"+strconv.Itoa(material.Id)+"/retry", "", material.UserId)
			c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(material.Id)}}
			RetryMaterial(c)
			var retried materialEnvelope
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &retried))
			require.Equal(t, tc.wantRetry, retried.Success, recorder.Body.String())
			if !tc.wantRetry {
				return
			}
			assert.Equal(t, "pending", retried.Data.Status)
			require.Len(t, retried.Data.Bindings, 1)
			assert.Equal(t, binding.AssetID, retried.Data.Bindings[0].AssetID)
			assert.Equal(t, binding.GroupID, retried.Data.Bindings[0].GroupID)
			provider := &materialPreparationProvider{status: "ACTIVE"}
			require.NoError(t, service.PrepareMaterial(t.Context(), &retried.Data, binding.Scope, provider))
			require.NoError(t, db.First(&material, material.Id).Error)
			assert.Equal(t, "ready", material.Status)
			assert.Zero(t, provider.groups)
			assert.Zero(t, provider.assets)
			assert.Equal(t, 1, provider.assetGets)
		})
	}
}

func TestAIGCMaterialUnresolvedAndTimeout(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, model.MigrateMaterialPreparation(db))
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "timeout", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&material).Error)
	provider := &materialPreparationProvider{status: "PROCESSING", createErr: context.DeadlineExceeded, omitLookup: true}
	require.NoError(t, service.PrepareMaterial(context.Background(), &material, "timeout-scope", provider))
	retried, err := model.RetryUserMaterial(users[1].Id, material.Id)
	require.NoError(t, err)
	require.NoError(t, service.PrepareMaterial(context.Background(), retried, "timeout-scope", provider))
	assert.Equal(t, 1, provider.assets, "unconfirmed uploads must not be repeated")
	require.NoError(t, db.First(&material, material.Id).Error)
	assert.Equal(t, "failed", material.Status)
	provider.omitLookup, provider.createErr = false, nil
	retried, err = model.RetryUserMaterial(users[1].Id, material.Id)
	require.NoError(t, err)
	require.NoError(t, service.PrepareMaterial(context.Background(), retried, "timeout-scope", provider))
	require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", material.Id).Update("started_at", common.GetTimestamp()-601).Error)
	require.NoError(t, db.First(&material, material.Id).Error)
	require.NoError(t, service.PrepareMaterial(context.Background(), &material, "timeout-scope", provider))
	require.NoError(t, db.First(&material, material.Id).Error)
	assert.Equal(t, "failed", material.Status)
	assert.Contains(t, material.FailReason, "timed out")
	retried, err = model.RetryUserMaterial(users[1].Id, material.Id)
	require.NoError(t, err)
	provider.getErr = context.DeadlineExceeded
	require.NoError(t, service.PrepareMaterial(context.Background(), retried, "timeout-scope", provider))
	require.NoError(t, db.First(&material, material.Id).Error)
	provider.getErr = nil
	require.NoError(t, service.PrepareMaterial(context.Background(), &material, "timeout-scope", provider))
	require.NoError(t, db.First(&material, material.Id).Error)
	assert.Equal(t, "processing", material.Status, "retry must reset the polling deadline even when the first query fails")
	assert.Empty(t, material.FailReason)
	assert.False(t, material.CanRetry)
	assert.Equal(t, 1, provider.assets)
}

func TestAIGCMaterialReadinessGate(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, model.MigrateMaterialPreparation(db))
	materials := []model.Material{
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "first", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "ready"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "second", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "processing"},
	}
	require.NoError(t, db.Create(&materials).Error)
	for i := range materials {
		require.NoError(t, db.Create(&model.MaterialBinding{MaterialId: materials[i].Id, Scope: "scope", Version: model.MaterialSourceVersion(&materials[i]), State: materials[i].Status, AssetID: fmt.Sprintf("asset-%d", i)}).Error)
	}
	references := []service.MaterialReference{{ID: materials[0].Id, Field: "content[0].image_url", Usage: "first_frame"}, {ID: materials[1].Id, Field: "content[1].image_url", Usage: "reference_image"}}
	resolved, issues, err := service.ResolvePreparedMaterials(context.Background(), users[1].Id, "scope", references)
	require.NoError(t, err)
	assert.Nil(t, resolved, "no partially prepared references may reach video submission")
	require.Len(t, issues, 1)
	assert.Equal(t, 1, issues[0].Index)
	assert.Equal(t, references[1].Field, issues[0].Field)
	assert.Equal(t, references[1].Usage, issues[0].Usage)
	assert.Equal(t, materials[1].MaterialID, issues[0].MaterialID)
	require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", materials[1].Id).Updates(map[string]any{"state": "failed", "fail_reason": "Unsupported image format"}).Error)
	resolved, issues, err = service.ResolvePreparedMaterials(context.Background(), users[1].Id, "scope", references)
	require.NoError(t, err)
	assert.Nil(t, resolved, "one failed material must withhold every video reference")
	require.Len(t, issues, 1)
	assert.Equal(t, 1, issues[0].Index)
	assert.Equal(t, materials[1].MaterialID, issues[0].MaterialID)
	assert.Equal(t, "failed", issues[0].Status)
	assert.Equal(t, "Unsupported image format", issues[0].Reason)
	require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", materials[1].Id).Updates(map[string]any{"state": "ready", "fail_reason": ""}).Error)
	resolved, issues, err = service.ResolvePreparedMaterials(context.Background(), users[1].Id, "scope", references)
	require.NoError(t, err)
	assert.Empty(t, issues)
	assert.Equal(t, []string{"asset://asset-0", "asset://asset-1"}, resolved)
	for _, tc := range []struct {
		userID int
		scope  string
	}{{users[2].Id, "scope"}, {users[1].Id, "other-scope"}} {
		resolved, issues, err = service.ResolvePreparedMaterials(context.Background(), tc.userID, tc.scope, references)
		require.NoError(t, err)
		assert.Nil(t, resolved)
		assert.Len(t, issues, 2)
	}
	config := &service.MobileCloudMaterialConfiguration{Endpoint: "https://ecloud.10086.cn/api/openapi-maas", AccessKey: "account", PoolID: "pool"}
	assert.NotEqual(t, service.MobileCloudMaterialScope(config, 1), service.MobileCloudMaterialScope(config, 2))
}

func TestAIGCMaterialRecoversAssetAfterUnknownCreateResult(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for _, tc := range []struct {
		name, status, wantState, wantReason string
		createErr, lookupErr                error
		matches, wantLists                  int
		wantRetry                           bool
	}{
		{name: "unique active asset", status: "ACTIVE", createErr: context.DeadlineExceeded, matches: 1, wantLists: 2, wantState: "ready"},
		{name: "unique processing asset is not ready", status: "PROCESSING", createErr: context.DeadlineExceeded, matches: 1, wantLists: 2, wantState: "processing"},
		{name: "no matching asset", createErr: context.DeadlineExceeded, wantLists: 1, wantState: "uncertain", wantReason: "Material upload result is unknown; retry to check without uploading again", wantRetry: true},
		{name: "multiple matching assets", createErr: context.DeadlineExceeded, matches: 2, wantLists: 2, wantState: "uncertain", wantReason: "Multiple matching materials require administrator review"},
		{name: "lookup fails", createErr: context.DeadlineExceeded, lookupErr: errors.New("lookup unavailable"), wantLists: 1, wantState: "uncertain", wantReason: "Material upload result could not be confirmed; retry to check without uploading again", wantRetry: true},
		{name: "mobile cloud rejection", createErr: &service.MobileCloudMaterialRejection{Message: "Unsupported image format"}, wantState: "failed", wantReason: "Material preparation rejected: Unsupported image format", wantRetry: true},
		{name: "TopenRouter rejection", createErr: &service.TopenRouterMaterialRejection{Message: "Unsupported image format"}, wantState: "failed", wantReason: "Material preparation rejected: Unsupported image format", wantRetry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: tc.name, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
			require.NoError(t, db.Create(&material).Error)
			binding := model.MaterialBinding{MaterialId: material.Id, Scope: material.MaterialID, Version: model.MaterialSourceVersion(&material), Name: "aigc-" + material.MaterialID, GroupID: "upstream-group", State: "pending", StartedAt: common.GetTimestamp()}
			require.NoError(t, db.Create(&binding).Error)
			pages := [][]service.MobileCloudAsset{{
				{ID: "wrong-name", GroupID: binding.GroupID, Name: "another-material", Type: "Image"},
				{ID: "wrong-group", GroupID: "another-group", Name: binding.Name, Type: "Image"},
				{ID: "wrong-type", GroupID: binding.GroupID, Name: binding.Name, Type: "Video"},
			}}
			if tc.matches > 0 {
				matches := []service.MobileCloudAsset{{ID: "upstream-asset", GroupID: binding.GroupID, Name: binding.Name, Type: "Image"}}
				if tc.matches > 1 {
					matches = append(matches, service.MobileCloudAsset{ID: "duplicate-asset", GroupID: binding.GroupID, Name: binding.Name, Type: "Image"})
				}
				pages = append(pages, matches)
			}
			gets := 0
			provider := &materialPreparationProvider{status: tc.status, createErr: tc.createErr, lookupErr: tc.lookupErr, assetPages: pages, onAssetGet: func() { gets++ }}
			require.NoError(t, service.PrepareMaterial(t.Context(), &material, binding.Scope, provider))
			require.NoError(t, db.First(&binding, binding.Id).Error)
			require.NoError(t, db.First(&material, material.Id).Error)
			assert.Equal(t, tc.wantState, binding.State)
			assert.Equal(t, tc.wantReason, binding.FailReason)
			assert.Equal(t, tc.wantRetry, binding.CanRetry)
			assert.Empty(t, binding.LockedBy)
			assert.Zero(t, binding.LockedUntil)
			assert.Equal(t, 1, provider.assets, "upload errors must never trigger another create")
			assert.Zero(t, provider.groups, "recovery must use the original group")
			assert.Equal(t, tc.wantLists, provider.assetLists)
			if tc.wantLists > 0 {
				assert.Equal(t, []string{binding.GroupID}, provider.assetQuery.GroupIDs)
				require.NotNil(t, provider.assetQuery.Name)
				assert.Equal(t, binding.Name, *provider.assetQuery.Name)
			}
			if tc.matches == 1 {
				assert.Equal(t, "upstream-asset", binding.AssetID)
				assert.Equal(t, tc.status, binding.UpstreamStatus)
				assert.Equal(t, tc.wantState, material.Status)
				assert.Equal(t, 1, gets)
				return
			}
			assert.Empty(t, binding.AssetID)
			assert.Equal(t, model.MaterialStatusFailed, material.Status)
			assert.Zero(t, gets, "unconfirmed assets must never reach the readiness check")
		})
	}
}

func TestAIGCMaterialGroupRecovery(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, model.MigrateMaterialPreparation(db))
	for _, failure := range []struct {
		name     string
		err      error
		rejected bool
	}{
		{"unknown result", context.DeadlineExceeded, false},
		{"mobile cloud rejection", &service.MobileCloudMaterialRejection{Message: "Group quota exceeded"}, true},
		{"TopenRouter rejection", &service.TopenRouterMaterialRejection{Message: "Group quota exceeded"}, true},
	} {
		t.Run(failure.name, func(t *testing.T) {
			rejected := failure.rejected
			material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "group recovery", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
			require.NoError(t, db.Create(&material).Error)
			provider := &materialPreparationProvider{status: "ACTIVE", groupErr: failure.err}
			require.NoError(t, service.PrepareMaterial(context.Background(), &material, material.MaterialID, provider))
			require.NoError(t, db.First(&material, material.Id).Error)
			assert.Equal(t, "failed", material.Status)
			if rejected {
				assert.Contains(t, material.FailReason, "Group quota exceeded")
			}
			retried, err := model.RetryUserMaterial(users[1].Id, material.Id)
			require.NoError(t, err)
			provider.groupErr = nil
			require.NoError(t, service.PrepareMaterial(context.Background(), retried, material.MaterialID, provider))
			require.NoError(t, db.First(&material, material.Id).Error)
			if !rejected {
				assert.Equal(t, "failed", material.Status)
				assert.Contains(t, material.FailReason, "unknown")
				assert.Equal(t, 1, provider.groups)
				assert.Zero(t, provider.assets)
				return
			}
			assert.Equal(t, "ready", material.Status)
			assert.Equal(t, 2, provider.groups)
			assert.Equal(t, 1, provider.assets)
		})
	}
}

func TestAIGCMaterialConfigurationFailure(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, model.MigrateMaterialPreparation(db))
	t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
	t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "")
	require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
	group := model.MaterialGroup{UserId: users[1].Id, Name: "Pending group without configuration"}
	require.NoError(t, model.SaveMaterialGroup(&group))
	require.NoError(t, db.Create(&model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: "missing-config", State: "pending"}).Error)
	require.NoError(t, db.Model(&model.Material{}).Where("status IN ?", []string{"pending", "processing"}).Update("status", "ready").Error)
	assert.True(t, (service.MaterialPreparationHandler{}).Enabled())
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "missing config", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&material).Error)
	require.NoError(t, db.Create(&model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: "missing-config", State: "pending"}).Error)
	unbound := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "no model selected", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?unbound", Status: "pending"}
	require.NoError(t, db.Create(&unbound).Error)
	deletedGroup := model.MaterialGroup{UserId: users[1].Id, Name: "never uploaded group", Status: "processing", Deleting: true}
	require.NoError(t, db.Create(&deletedGroup).Error)
	deleted := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, LibraryType: "AIGC", FileType: "Image", Status: "processing", Deleting: true}
	require.NoError(t, db.Create(&deleted).Error)
	require.Error(t, service.RunMaterialPreparationOnce(context.Background()))
	var count int64
	require.NoError(t, db.Model(&model.MaterialGroup{}).Where("id = ?", deletedGroup.Id).Count(&count).Error)
	assert.Zero(t, count, "unbound group deletion needs no supplier configuration")
	require.NoError(t, db.Model(&model.Material{}).Where("id = ?", deleted.Id).Count(&count).Error)
	assert.Zero(t, count, "unbound material deletion needs no supplier configuration")
	require.NoError(t, db.First(&unbound, unbound.Id).Error)
	assert.Equal(t, "pending", unbound.Status)
	assert.Empty(t, unbound.FailReason)
	require.NoError(t, db.First(&group, group.Id).Error)
	assert.Equal(t, "failed", group.Status)
	assert.Contains(t, group.FailReason, "configuration")
	require.NoError(t, db.First(&material, material.Id).Error)
	assert.Equal(t, "failed", material.Status)
	assert.True(t, material.CanRetry)
	assert.Contains(t, material.FailReason, "configuration")
}

func TestAIGCMaterialMigration(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	legacy := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "legacy", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending_auth"}
	require.NoError(t, db.Omit("fail_reason", "can_retry").Create(&legacy).Error)
	group := model.MaterialGroup{UserId: users[1].Id, Name: "Legacy group"}
	require.NoError(t, model.SaveMaterialGroup(&group))
	// Newly added nullable columns on pre-existing records need a portable
	// backfill, not a database-specific boolean default.
	require.NoError(t, db.Model(&group).Updates(map[string]any{"status": nil, "deleting": nil}).Error)
	require.NoError(t, db.Model(&legacy).Update("deleting", nil).Error)
	for range 2 {
		require.NoError(t, model.MigrateMaterialPreparation(db))
	}
	require.NoError(t, db.First(&group, group.Id).Error)
	assert.Equal(t, "pending", group.Status)
	var groupCount int64
	require.NoError(t, db.Model(&model.MaterialGroup{}).Where("id = ? AND deleting = ?", group.Id, false).Count(&groupCount).Error)
	assert.EqualValues(t, 1, groupCount)
	var migrated model.Material
	require.NoError(t, db.First(&migrated, legacy.Id).Error)
	legacy.Status = "pending"
	assert.Equal(t, legacy, migrated)
}

func TestAIGCMaterialSourcePolicy(t *testing.T) {
	fetch := system_setting.GetFetchSetting()
	previous := *fetch
	*fetch = system_setting.FetchSetting{EnableSSRFProtection: false, AllowPrivateIp: true}
	t.Cleanup(func() { *fetch = previous })
	for _, source := range []string{"http://8.8.8.8/image", "https://127.0.0.1/image", "https://10.0.0.1/image", "https://user:password@8.8.8.8/image", "https://8.8.8.8/image#fragment", "https://8.8.8.8/image#", "https:///image"} {
		assert.Error(t, service.ValidateMaterialPreparationSource(source), source)
	}
	assert.NoError(t, service.ValidateMaterialPreparationSource(publicMaterialURL))
}

func TestAIGCMaterialChannelSync(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, model.MigrateMaterialPreparation(db))
	t.Run("partial channel failures retry without repeating success and use real group IDs", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Synchronized portraits"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		first := &materialPreparationProvider{status: "ACTIVE"}
		second := &materialPreparationProvider{status: "ACTIVE", createErr: &service.MobileCloudMaterialRejection{Message: "Unsupported image format"}}
		channels := []service.MaterialChannel{
			{ChannelID: 101, ChannelName: "first channel", Provider: "mobile_cloud", Scope: "sync-first", Client: first},
			{ChannelID: 102, ChannelName: "second channel", Provider: "mobile_cloud", Scope: "sync-second", Client: second},
		}
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &group.Id, Name: "portrait", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
		require.NoError(t, db.Create(&material).Error)
		for _, channel := range channels {
			require.NoError(t, db.Create(&model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: channel.Scope, ChannelID: channel.ChannelID, ChannelName: channel.ChannelName, Provider: channel.Provider, Name: "aigc-" + material.MaterialID, State: "pending", StartedAt: common.GetTimestamp()}).Error)
		}
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "failed", material.Status)
		assert.Contains(t, material.FailReason, "second channel")
		assert.Equal(t, "upstream-group", first.input.GroupID)
		assert.Equal(t, "upstream-group", second.input.GroupID)
		assert.Equal(t, group.Name, first.groupName)
		assert.Equal(t, 1, first.groups)
		assert.Equal(t, 1, second.groups)
		assert.False(t, first.groupDescriptionProvided, "group creation must omit the description parameter")
		assert.False(t, second.groupDescriptionProvided, "group creation must omit the description parameter")
		var bindings []model.MaterialBinding
		require.NoError(t, db.Where("material_id = ?", material.Id).Order("channel_id").Find(&bindings).Error)
		require.Len(t, bindings, 2)
		assert.Equal(t, "ACTIVE", bindings[0].UpstreamStatus)
		assert.Equal(t, 101, bindings[0].ChannelID)
		assert.Equal(t, "mobile_cloud", bindings[0].Provider)
		second.createErr = nil
		retried, err := model.RetryUserMaterial(material.UserId, material.Id)
		require.NoError(t, err)
		require.NoError(t, service.SyncMaterial(context.Background(), retried, channels))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "ready", material.Status)
		assert.Equal(t, 1, first.assets, "successful channels must never upload twice")
		assert.Equal(t, 2, second.assets)
		group.Name = "Renamed portraits"
		require.NoError(t, model.SaveMaterialGroup(&group))
		second.groupErr = context.DeadlineExceeded
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "failed", group.Status)
		var mapping model.MaterialGroupBinding
		require.NoError(t, db.Where("group_id = ? AND scope = ?", group.Id, channels[1].Scope).First(&mapping).Error)
		assert.Equal(t, "upstream-group", mapping.UpstreamID, "failed rename retains the original mapping")
		second.groupErr = nil
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "ready", group.Status)
		assert.Equal(t, 1, first.updates)
		assert.Equal(t, 2, second.updates)
		listed, err := model.ListUserMaterialGroups(group.UserId)
		require.NoError(t, err)
		var found *model.MaterialGroup
		for _, item := range listed {
			if item.Id == group.Id {
				found = item
			}
		}
		require.NotNil(t, found)
		require.Len(t, found.Bindings, 2)
		body, err := common.Marshal(found)
		require.NoError(t, err)
		assert.NotContains(t, string(body), channels[0].Scope)
		assert.NotContains(t, string(body), "locked_by")
		assert.NotContains(t, string(body), "Scope")

		group.Deleting = true
		require.NoError(t, db.Model(&group).Update("deleting", true).Error)
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		assert.Zero(t, first.groupDeletes, "local materials block group deletion")
		assert.Zero(t, second.groupDeletes)
		material.Deleting = true
		require.NoError(t, db.Model(&material).Updates(map[string]any{"deleting": true, "status": "processing"}).Error)
		second.deleteErr = context.DeadlineExceeded
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.True(t, material.Deleting)
		assert.Equal(t, "failed", material.Status)
		assert.Equal(t, 1, first.assetDeletes)
		assert.Equal(t, 1, second.assetDeletes)
		require.NoError(t, db.Where("material_id = ?", material.Id).Order("channel_id").Find(&bindings).Error)
		require.Len(t, bindings, 2, "partial deletion retains all mappings")
		assert.Equal(t, "deleted", bindings[0].State)
		second.deleteErr = nil
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		var count int64
		require.NoError(t, db.Model(&model.Material{}).Where("id = ?", material.Id).Count(&count).Error)
		assert.Zero(t, count)
		assert.Equal(t, 1, first.assetDeletes)
		assert.Equal(t, 2, second.assetDeletes)
		assert.Positive(t, second.assetLists, "unknown deletion must query before repeating DELETE")
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.Model(&model.MaterialGroup{}).Where("id = ?", group.Id).Count(&count).Error)
		assert.Zero(t, count)
		assert.Equal(t, 1, first.groupDeletes)
		assert.Equal(t, 1, second.groupDeletes)
	})
	t.Run("preparation reloads the group after a queued local move", func(t *testing.T) {
		first := model.MaterialGroup{UserId: users[1].Id, Name: "Before move"}
		second := model.MaterialGroup{UserId: users[1].Id, Name: "After move"}
		require.NoError(t, model.SaveMaterialGroup(&first))
		require.NoError(t, model.SaveMaterialGroup(&second))
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &first.Id, Name: "moved image", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
		require.NoError(t, db.Create(&material).Error)
		_, err := model.SetUserMaterialGroup(users[1].Id, material.Id, &second.Id)
		require.NoError(t, err)
		binding := model.MaterialGroupBinding{GroupId: second.Id, UserId: users[1].Id, Scope: "move-snapshot", Name: second.Name, UpstreamID: "upstream-group", State: "ready"}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{status: "ACTIVE"}
		require.NoError(t, service.PrepareMaterial(context.Background(), &material, binding.Scope, provider))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "ready", material.Status)
		assert.Equal(t, second.Id, *material.GroupId)
		assert.Equal(t, "upstream-group", provider.input.GroupID)
		_, err = model.SetUserMaterialGroup(users[1].Id, material.Id, &first.Id)
		require.Error(t, err, "upload claim must block subsequent local-only moves")
	})
	t.Run("a rename during synchronization remains queued", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			initial string
			updated string
		}{
			{name: "different name", initial: "Concurrent rename", updated: "Latest requested name"},
			{name: "case only", initial: "Case rename", updated: "case rename"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				group := model.MaterialGroup{UserId: users[1].Id, Name: tc.initial}
				require.NoError(t, model.SaveMaterialGroup(&group))
				provider := &materialPreparationProvider{status: "ACTIVE"}
				provider.onGroupCreate = func() {
					renamed := model.MaterialGroup{Id: group.Id, UserId: group.UserId, Name: tc.updated}
					require.NoError(t, model.SaveMaterialGroup(&renamed))
				}
				channels := []service.MaterialChannel{{ChannelID: 211, ChannelName: "rename channel", Scope: "rename-race", Client: provider}}
				require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
				require.NoError(t, db.First(&group, group.Id).Error)
				assert.Equal(t, "pending", group.Status, "an older synchronization must not consume a new rename")
				provider.onGroupCreate = nil
				require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
				require.NoError(t, db.First(&group, group.Id).Error)
				assert.Equal(t, "ready", group.Status)
				assert.Equal(t, tc.updated, provider.groupName)
			})
		}
	})
	t.Run("unknown creation never adopts another owners same-name group", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Shared group name"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		provider := &materialPreparationProvider{groupErr: context.DeadlineExceeded}
		channels := []service.MaterialChannel{{ChannelID: 212, ChannelName: "isolated channel", Scope: "same-name-recovery", Client: provider}}
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		provider.groupErr = nil
		provider.groupPages = [][]service.MobileCloudAssetGroup{{{ID: "another-owners-group", Type: "AIGC", Name: group.Name, Description: "another owners description"}}}
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		var binding model.MaterialGroupBinding
		require.NoError(t, db.Where("group_id = ?", group.Id).First(&binding).Error)
		assert.Empty(t, binding.UpstreamID)
		assert.Zero(t, provider.updates)
		assert.Equal(t, 1, provider.groups)
		group.Deleting = true
		require.NoError(t, db.Model(&group).Update("deleting", true).Error)
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		assert.Zero(t, provider.groupDeletes)
	})
	t.Run("unknown group creation only recovers and never blindly creates", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Unknown create"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		provider := &materialPreparationProvider{groupErr: context.DeadlineExceeded, omitLookup: true}
		channels := []service.MaterialChannel{{ChannelID: 201, ChannelName: "recover", Provider: "mobile_cloud", Scope: "unknown-group", Client: provider}}
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		assert.Equal(t, 1, provider.groups)
		assert.Positive(t, provider.groupLists)
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "failed", group.Status)
		provider.omitLookup, provider.groupErr = false, nil
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "failed", group.Status)
		assert.Contains(t, group.FailReason, "unknown")
		assert.Equal(t, 1, provider.groups)
		assert.Zero(t, provider.updates)
	})
	t.Run("legacy marked creation recovers and deletion queries before retry", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Unknown legacy create"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		binding := model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: "legacy-group", Name: group.Name, CreationToken: "new-api-material-group:legacy-test", State: "creating"}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{groupName: group.Name, groupDescription: binding.CreationToken}
		channels := []service.MaterialChannel{{ChannelID: 202, Scope: binding.Scope, Client: provider}}
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "ready", group.Status)
		assert.Zero(t, provider.groups)
		group.Deleting = true
		require.NoError(t, db.Model(&group).Update("deleting", true).Error)
		provider.deleteErr = context.DeadlineExceeded
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		provider.deleteErr, provider.groupDeleted = nil, true
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		assert.Equal(t, 1, provider.groupDeletes, "confirmed absent remote group must not be deleted twice")
		var count int64
		require.NoError(t, db.Model(&model.MaterialGroup{}).Where("id = ?", group.Id).Count(&count).Error)
		assert.Zero(t, count)
	})
	t.Run("old scopes never use new credentials for upload or deletion", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Old resource space"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		require.NoError(t, db.Create(&model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: "old-credentials", ChannelID: 301, UpstreamID: "old-group", Name: group.Name, State: "ready"}).Error)
		provider := &materialPreparationProvider{status: "ACTIVE"}
		channels := []service.MaterialChannel{{ChannelID: 301, ChannelName: "changed account", Provider: "mobile_cloud", Scope: "new-credentials", Client: provider}}
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "failed", group.Status)
		assert.Zero(t, provider.groups)
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "old asset", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "processing", Deleting: true}
		require.NoError(t, db.Create(&material).Error)
		require.NoError(t, db.Create(&model.MaterialBinding{MaterialId: material.Id, Version: "historical-version", Scope: "old-credentials", ChannelID: 301, GroupID: "old-group", AssetID: "old-asset", State: "ready"}).Error)
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "failed", material.Status)
		assert.Contains(t, material.FailReason, "configuration")
		assert.Zero(t, provider.assetDeletes)
		assert.Zero(t, provider.assets)
	})
	t.Run("deleting materials cannot upload or resolve ready assets", func(t *testing.T) {
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "deleting material", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "processing", Deleting: true}
		require.NoError(t, db.Create(&material).Error)
		provider := &materialPreparationProvider{status: "ACTIVE"}
		require.NoError(t, service.PrepareMaterial(context.Background(), &material, "deleting-scope", provider))
		assert.Zero(t, provider.groups)
		assert.Zero(t, provider.assets)
		binding := model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: "deleting-scope", GroupID: "upstream-group", AssetID: "upstream-asset", State: "ready"}
		require.NoError(t, db.Create(&binding).Error)
		resolved, issues, err := service.ResolvePreparedMaterials(context.Background(), material.UserId, binding.Scope, []service.MaterialReference{{ID: material.Id}})
		require.NoError(t, err)
		assert.Empty(t, resolved)
		require.Len(t, issues, 1)
		assert.Equal(t, "unavailable", issues[0].Status)
	})
	t.Run("deletion uses the preparation lease and only DELETE retries deletion", func(t *testing.T) {
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "leased deletion", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "processing", Deleting: true}
		require.NoError(t, db.Create(&material).Error)
		binding := model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: "delete-lease", GroupID: "upstream-group", AssetID: "upstream-asset", State: "ready", LockedBy: "preparation-worker", LockedUntil: common.GetTimestamp() + 120}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{status: "ACTIVE"}
		channels := []service.MaterialChannel{{ChannelID: 901, Scope: binding.Scope, Client: provider}}
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		assert.Zero(t, provider.assetDeletes, "deletion cannot race an active preparation lease")
		require.NoError(t, db.First(&binding, binding.Id).Error)
		assert.Equal(t, "preparation-worker", binding.LockedBy)
		require.NoError(t, db.Model(&material).Updates(map[string]any{"status": "failed", "can_retry": true}).Error)
		_, err := model.RetryUserMaterial(material.UserId, material.Id)
		require.Error(t, err, "upload retry is not a deletion retry")
		require.NoError(t, db.Model(&binding).Update("locked_until", common.GetTimestamp()-1).Error)
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		assert.Equal(t, 1, provider.assetDeletes)
		var count int64
		require.NoError(t, db.Model(&model.Material{}).Where("id = ?", material.Id).Count(&count).Error)
		assert.Zero(t, count)
	})
	t.Run("explicit group rejection requires a user retry", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Rejected group"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		provider := &materialPreparationProvider{groupErr: &service.MobileCloudMaterialRejection{Message: "Group quota exceeded"}}
		channels := []service.MaterialChannel{{ChannelID: 701, Scope: "rejected-group", Client: provider}}
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "failed", group.Status)
		provider.groupErr = nil
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		assert.Equal(t, 1, provider.groups)
		require.NoError(t, db.Model(&group).Update("status", "pending").Error)
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "ready", group.Status)
		assert.Equal(t, 2, provider.groups)
	})
	t.Run("remote assets prevent empty local group deletion", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Remote nonempty group"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		provider := &materialPreparationProvider{assets: 1, input: service.MobileCloudCreateAssetInput{GroupID: "upstream-group"}}
		channels := []service.MaterialChannel{{ChannelID: 801, Scope: "nonempty-remote", Client: provider}}
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.Model(&group).Update("deleting", true).Error)
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "failed", group.Status)
		assert.Contains(t, group.FailReason, "not empty")
		assert.Zero(t, provider.groupDeletes)
		provider.assetDeleted, provider.deleteFalse = true, true
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "failed", group.Status)
		assert.Equal(t, 1, provider.groupDeletes)
		provider.deleteFalse = false
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		var count int64
		require.NoError(t, db.Model(&model.MaterialGroup{}).Where("id = ?", group.Id).Count(&count).Error)
		assert.Zero(t, count)
		assert.Equal(t, 2, provider.groupDeletes)
	})
	t.Run("delete false and lookup errors retain mappings until absence is proven", func(t *testing.T) {
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "delete confirmation", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "processing", Deleting: true}
		require.NoError(t, db.Create(&material).Error)
		binding := model.MaterialBinding{MaterialId: material.Id, Version: "old-version", Scope: "delete-confirm", Name: "stable-delete-name", GroupID: "upstream-group", AssetID: "upstream-asset", State: "ready"}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{deleteFalse: true, assets: 1, input: service.MobileCloudCreateAssetInput{GroupID: "upstream-group", Name: binding.Name}}
		channels := []service.MaterialChannel{{ChannelID: 401, ChannelName: "delete target", Scope: binding.Scope, Client: provider}}
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		require.NoError(t, db.First(&binding, binding.Id).Error)
		assert.Equal(t, "deleting", binding.State)
		assert.Equal(t, 1, provider.assetDeletes)
		provider.lookupErr = &service.MobileCloudMaterialRejection{Message: "HTTP status 404"}
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "failed", material.Status)
		assert.Equal(t, 1, provider.assetDeletes, "404 must never prove absence or authorize blind DELETE")
		provider.lookupErr, provider.assetDeleted = nil, true
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		var count int64
		require.NoError(t, db.Model(&model.Material{}).Where("id = ?", material.Id).Count(&count).Error)
		assert.Zero(t, count)
		assert.Equal(t, 1, provider.assetDeletes)
	})
	t.Run("unknown asset creation is recovered across pages before deletion", func(t *testing.T) {
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "unknown upload", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "processing", Deleting: true}
		require.NoError(t, db.Create(&material).Error)
		binding := model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: "paged-delete", Name: "stable-paged-name", GroupID: "upstream-group", State: "creating"}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{assetPages: [][]service.MobileCloudAsset{
			{{ID: "unrelated-asset", GroupID: "upstream-group", Name: "other", Type: "Image"}},
			{{ID: "upstream-asset", GroupID: "upstream-group", Name: binding.Name, Type: "Image"}},
		}}
		channels := []service.MaterialChannel{{ChannelID: 501, Scope: binding.Scope, Client: provider}}
		require.NoError(t, service.SyncMaterial(context.Background(), &material, channels))
		assert.Equal(t, 2, provider.assetLists)
		assert.Equal(t, 1, provider.assetDeletes)
		assert.Zero(t, provider.assets)
		var count int64
		require.NoError(t, db.Model(&model.Material{}).Where("id = ?", material.Id).Count(&count).Error)
		assert.Zero(t, count)
	})
	t.Run("an unavailable group must not turn an unknown upload into permission to reupload", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Unready group with unknown upload"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, GroupId: &group.Id, Name: "unknown asset", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
		require.NoError(t, db.Create(&material).Error)
		binding := model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: "unready-recovery", Name: "unknown-stable-name", GroupID: "upstream-group", State: "uncertain", StartedAt: common.GetTimestamp()}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{status: "ACTIVE", omitLookup: true}
		require.NoError(t, service.PrepareMaterial(context.Background(), &material, binding.Scope, provider))
		require.NoError(t, db.First(&binding, binding.Id).Error)
		assert.Equal(t, "uncertain", binding.State)
		require.NoError(t, db.Create(&model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: binding.Scope, UpstreamID: "upstream-group", Name: group.Name, State: "ready"}).Error)
		require.NoError(t, service.PrepareMaterial(context.Background(), &material, binding.Scope, provider))
		assert.Zero(t, provider.assets, "recovering the group must never authorize repeating an unknown upload")
		assert.Positive(t, provider.assetLists)
	})
	t.Run("ambiguous upload recovery never adopts the first matching asset", func(t *testing.T) {
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "ambiguous upload", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
		require.NoError(t, db.Create(&material).Error)
		binding := model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: "ambiguous-upload", Name: "ambiguous-stable-name", GroupID: "upstream-group", State: "uncertain", StartedAt: common.GetTimestamp()}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{status: "ACTIVE", assetPages: [][]service.MobileCloudAsset{{
			{ID: "upstream-asset", GroupID: "upstream-group", Name: binding.Name, Type: "Image"},
			{ID: "other-matching-asset", GroupID: "upstream-group", Name: binding.Name, Type: "Image"},
		}}}
		for range 2 {
			require.NoError(t, service.PrepareMaterial(context.Background(), &material, binding.Scope, provider))
			require.NoError(t, db.First(&material, material.Id).Error)
			assert.Equal(t, "failed", material.Status)
		}
		require.NoError(t, db.First(&binding, binding.Id).Error)
		assert.Empty(t, binding.AssetID)
		assert.Zero(t, provider.assets)
	})
	t.Run("direct preparation recovers an upload from a complete paginated listing", func(t *testing.T) {
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "paged upload", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
		require.NoError(t, db.Create(&material).Error)
		binding := model.MaterialBinding{MaterialId: material.Id, Version: model.MaterialSourceVersion(&material), Scope: "paged-upload", Name: "stable-upload-name", GroupID: "upstream-group", State: "uncertain", StartedAt: common.GetTimestamp()}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{status: "ACTIVE", assetPages: [][]service.MobileCloudAsset{
			{{ID: "unrelated-asset", GroupID: "upstream-group", Name: "other", Type: "Image"}},
			{{ID: "upstream-asset", GroupID: "upstream-group", Name: binding.Name, Type: "Image"}},
		}}
		require.NoError(t, service.PrepareMaterial(context.Background(), &material, binding.Scope, provider))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, "ready", material.Status)
		assert.Zero(t, provider.assets)
		assert.Equal(t, 2, provider.assetLists)
	})
	t.Run("complete paginated recovery may adopt exactly one group", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Paged group"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		binding := model.MaterialGroupBinding{GroupId: group.Id, UserId: group.UserId, Scope: "paged-group", Name: group.Name, CreationToken: "new-api-material-group:paged-test", State: "creating"}
		require.NoError(t, db.Create(&binding).Error)
		provider := &materialPreparationProvider{groupPages: [][]service.MobileCloudAssetGroup{
			{{ID: "wrong-type", Name: group.Name, Type: "LivenessFace"}},
			{{ID: "upstream-group", Name: group.Name, Type: "AIGC", Description: binding.CreationToken}},
		}}
		channels := []service.MaterialChannel{{ChannelID: 601, Scope: binding.Scope, Client: provider}}
		require.NoError(t, service.SyncMaterialGroup(context.Background(), &group, channels))
		require.NoError(t, db.First(&group, group.Id).Error)
		assert.Equal(t, "ready", group.Status)
		assert.Zero(t, provider.groups)
		assert.Equal(t, 2, provider.groupLists)
	})
	t.Run("loader exposes only the explicitly configured enabled material channel", func(t *testing.T) {
		t.Setenv("MOBILE_CLOUD_MATERIAL_TEST_ENABLED", "true")
		t.Setenv("MOBILE_CLOUD_MATERIAL_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
		channel := model.Channel{Name: "Configured material adapter", Status: 1, Key: "not-a-material-credential"}
		require.NoError(t, db.Create(&channel).Error)
		config := service.MobileCloudMaterialConfiguration{ChannelID: channel.Id, Endpoint: service.MobileCloudMaterialDefaultEndpoint, AccessKey: "fake-access-key", SecretKey: "fake-secret-key", PoolID: "fake-pool"}
		// Isolate encrypted configurations within the rollback-only fixture transaction.
		require.NoError(t, db.Where("key IN ?", []string{model.MobileCloudMaterialConfigOption, model.TopenRouterMaterialConfigOption}).Delete(&model.Option{}).Error)
		require.NoError(t, service.SaveMobileCloudMaterialConfiguration(config))
		channels, err := service.LoadMaterialChannels()
		require.NoError(t, err)
		require.Len(t, channels, 1)
		assert.Equal(t, channel.Id, channels[0].ChannelID)
		assert.Equal(t, channel.Name, channels[0].ChannelName)
		assert.Equal(t, "mobile_cloud", channels[0].Provider)
		assert.Equal(t, service.MobileCloudMaterialScope(&config, channel.Id), channels[0].Scope)
		require.NoError(t, db.Model(&channel).Update("status", 2).Error)
		_, err = service.LoadMaterialChannels()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), config.AccessKey)
		assert.NotContains(t, err.Error(), config.SecretKey)
		require.NoError(t, db.Delete(&channel).Error)
		_, err = service.LoadMaterialChannels()
		require.Error(t, err)
	})
	t.Run("group changes enqueue upstream synchronization", func(t *testing.T) {
		group := model.MaterialGroup{UserId: users[1].Id, Name: "Queued group"}
		require.NoError(t, model.SaveMaterialGroup(&group))
		var stored map[string]any
		require.NoError(t, db.Model(&model.MaterialGroup{}).Where("id = ?", group.Id).Take(&stored).Error)
		assert.Equal(t, "pending", stored["status"])
		assert.True(t, db.Migrator().HasTable(&model.MaterialGroupBinding{}))
	})
	t.Run("one ready channel cannot hide another failed channel", func(t *testing.T) {
		material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "multi-channel", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
		require.NoError(t, db.Create(&material).Error)
		require.NoError(t, db.Create(&[]model.MaterialBinding{
			{MaterialId: material.Id, Scope: "first", Version: model.MaterialSourceVersion(&material), State: "ready", AssetID: "asset-first"},
			{MaterialId: material.Id, Scope: "second", Version: model.MaterialSourceVersion(&material), State: "failed", FailReason: "Unsupported image format", CanRetry: true},
		}).Error)
		require.NoError(t, service.PrepareMaterial(context.Background(), &material, "first", &materialPreparationProvider{}))
		require.NoError(t, db.First(&material, material.Id).Error)
		assert.Equal(t, model.MaterialStatusFailed, material.Status)
		assert.Contains(t, material.FailReason, "Unsupported image format")
		assert.True(t, material.CanRetry)
	})
}

type materialPreparationProvider struct {
	groups, assets                      int
	status, reason                      string
	createErr                           error
	groupErr                            error
	getErr                              error
	omitLookup                          bool
	input                               service.MobileCloudCreateAssetInput
	groupName                           string
	groupID                             string
	groupDescription                    string
	groupDescriptionProvided            bool
	onGroupCreate                       func()
	onAssetCreate                       func()
	onAssetGet                          func()
	updates, groupDeletes, assetDeletes int
	groupLists, assetLists              int
	deleteErr                           error
	deleteFalse                         bool
	groupDeleted, assetDeleted          bool
	lookupErr                           error
	assetPages                          [][]service.MobileCloudAsset
	assetQuery                          service.MobileCloudAssetQuery
	groupPages                          [][]service.MobileCloudAssetGroup
	assetResponse                       *service.MobileCloudAsset
	assetGets                           int
}

func (p *materialPreparationProvider) GetAssetGroup(context.Context, string) (*service.MobileCloudAssetGroup, error) {
	return &service.MobileCloudAssetGroup{ID: "upstream-group", Type: "AIGC", Name: p.groupName}, p.getErr
}
func (p *materialPreparationProvider) UpdateAssetGroup(_ context.Context, _ string, input service.MobileCloudGroupUpdate) (*service.MobileCloudAssetGroup, error) {
	p.updates++
	if p.groupErr != nil {
		return nil, p.groupErr
	}
	p.groupName = *input.Name
	return &service.MobileCloudAssetGroup{ID: "upstream-group", Type: "AIGC", Name: p.groupName}, nil
}
func (p *materialPreparationProvider) DeleteAssetGroup(context.Context, string) (bool, error) {
	p.groupDeletes++
	if p.deleteErr != nil || p.deleteFalse {
		return false, p.deleteErr
	}
	p.groupDeleted = true
	return true, nil
}
func (p *materialPreparationProvider) DeleteAsset(context.Context, string) (bool, error) {
	p.assetDeletes++
	if p.deleteErr != nil || p.deleteFalse {
		return false, p.deleteErr
	}
	p.assetDeleted = true
	return true, nil
}

func (p *materialPreparationProvider) CreateAssetGroup(_ context.Context, name string, description *string) (*service.MobileCloudAssetGroup, error) {
	p.groups++
	p.groupName = name
	p.groupDescriptionProvided = description != nil
	if description != nil {
		p.groupDescription = *description
	}
	if p.onGroupCreate != nil {
		p.onGroupCreate()
	}
	id := p.groupID
	if id == "" {
		id = "upstream-group"
	}
	return &service.MobileCloudAssetGroup{ID: id, Type: "AIGC"}, p.groupErr
}
func (p *materialPreparationProvider) ListAssetGroups(_ context.Context, query service.MobileCloudGroupQuery) (*service.MobileCloudMaterialPage[service.MobileCloudAssetGroup], error) {
	p.groupLists++
	page := &service.MobileCloudMaterialPage[service.MobileCloudAssetGroup]{}
	if p.groupPages != nil {
		for _, items := range p.groupPages {
			page.Total += int64(len(items))
		}
		index := int(*query.PageNo) - 1
		if index < len(p.groupPages) {
			page.Data = p.groupPages[index]
		}
		return page, p.lookupErr
	}
	if !p.groupDeleted && !p.omitLookup {
		page.Data = []service.MobileCloudAssetGroup{{ID: "upstream-group", Type: "AIGC", Name: p.groupName, Description: p.groupDescription}}
		page.Total = 1
	}
	return page, p.lookupErr
}
func (p *materialPreparationProvider) CreateAsset(_ context.Context, input service.MobileCloudCreateAssetInput) (string, error) {
	p.assets++
	p.input = input
	if p.onAssetCreate != nil {
		p.onAssetCreate()
	}
	return "upstream-asset", p.createErr
}
func (p *materialPreparationProvider) ListAssets(_ context.Context, query service.MobileCloudAssetQuery) (*service.MobileCloudMaterialPage[service.MobileCloudAsset], error) {
	p.assetLists++
	p.assetQuery = query
	page := &service.MobileCloudMaterialPage[service.MobileCloudAsset]{}
	if p.assetPages != nil {
		for _, items := range p.assetPages {
			page.Total += int64(len(items))
		}
		index := int(*query.PageNo) - 1
		if index < len(p.assetPages) {
			page.Data = p.assetPages[index]
		}
		return page, p.lookupErr
	}
	if !p.omitLookup && !p.assetDeleted && p.assets > 0 {
		page.Data = []service.MobileCloudAsset{{ID: "upstream-asset", GroupID: p.input.GroupID, Name: p.input.Name, Type: "Image"}}
		page.Total = 1
	}
	return page, p.lookupErr
}
func (p *materialPreparationProvider) GetAsset(context.Context, string) (*service.MobileCloudAsset, error) {
	p.assetGets++
	if p.onAssetGet != nil {
		p.onAssetGet()
	}
	if p.assetResponse != nil {
		return p.assetResponse, p.getErr
	}
	groupID := p.groupID
	if groupID == "" {
		groupID = "upstream-group"
	}
	return &service.MobileCloudAsset{ID: "upstream-asset", GroupID: groupID, Type: "Image", Status: p.status, ErrorMessage: p.reason}, p.getErr
}

type adminMaterialResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Total   int64           `json:"total"`
}

func newAdminMaterialRouter(t *testing.T) *gin.Engine {
	t.Helper()
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		if c.GetInt("id") != 0 {
			identity, ok := middleware.GetAuthIdentity(c)
			require.True(t, ok)
			assert.Equal(t, identity.UserID, c.GetInt("id"), "the target owner must never replace the authenticated actor")
		}
	})
	RegisterMaterialLibraryRoutes(router.Group("/api"))
	return router
}

func requestAdminMaterial(t *testing.T, router *gin.Engine, actor *model.User, method, target, body string, status int, success bool) adminMaterialResponse {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if actor != nil {
		request.Header.Set("Authorization", "Bearer "+actor.GetAccessToken())
	}
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, status, recorder.Code, "%s %s", method, target)
	var response adminMaterialResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, success, response.Success, "%s %s: %s", method, target, response.Message)
	if !success {
		assert.Empty(t, response.Data, "denied operations must not return owner data")
	}
	return response
}

func TestAdminMaterialManagement(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Material{}))
	router := newAdminMaterialRouter(t)
	admin, owner, other := &users[0], &users[1], &users[2]
	base := fmt.Sprintf("/api/material/admin/users/%d", owner.Id)
	otherBase := fmt.Sprintf("/api/material/admin/users/%d", other.Id)

	response := requestAdminMaterial(t, router, admin, http.MethodPost, base+"/groups", `{"name":"  Portraits  "}`, http.StatusOK, true)
	var group model.MaterialGroup
	require.NoError(t, common.Unmarshal(response.Data, &group))
	assert.Equal(t, owner.Id, group.UserId)
	assert.Equal(t, "Portraits", group.Name)
	requestAdminMaterial(t, router, admin, http.MethodPost, base+"/groups", `{"name":"portraits"}`, http.StatusOK, false)
	requestAdminMaterial(t, router, admin, http.MethodPost, base+"/groups", `{"name":"   "}`, http.StatusOK, false)
	response = requestAdminMaterial(t, router, admin, http.MethodPost, otherBase+"/groups", `{"name":"Portraits"}`, http.StatusOK, true)
	var foreign model.MaterialGroup
	require.NoError(t, common.Unmarshal(response.Data, &foreign))
	response = requestAdminMaterial(t, router, admin, http.MethodGet, base+"/groups", "", http.StatusOK, true)
	var groups []model.MaterialGroup
	require.NoError(t, common.Unmarshal(response.Data, &groups))
	require.Len(t, groups, 1)
	assert.Equal(t, group, groups[0])

	body := fmt.Sprintf(`{"name":"  hero  ","library_type":"AIGC","file_type":"Image","source_url":%q,"group_id":%d,"user_id":%d}`, publicMaterialURL, group.Id, other.Id)
	response = requestAdminMaterial(t, router, admin, http.MethodPost, base+"/materials", body, http.StatusOK, true)
	var material model.Material
	require.NoError(t, common.Unmarshal(response.Data, &material))
	assert.Equal(t, owner.Id, material.UserId)
	assert.Equal(t, "hero", material.Name)
	assert.Equal(t, "Portraits", material.GroupName)
	assert.Equal(t, &group.Id, material.GroupId)
	assert.Equal(t, model.MaterialStatusPending, material.Status)
	assert.Equal(t, model.MaterialFileImage, material.FileType)
	assert.Equal(t, model.MaterialLibraryAIGC, material.LibraryType)
	assert.True(t, strings.HasPrefix(material.MaterialID, "mat_"))
	assert.Positive(t, material.CreatedTime)
	path := fmt.Sprintf("%s/materials/%d", base, material.Id)
	response = requestAdminMaterial(t, router, admin, http.MethodGet, path, "", http.StatusOK, true)
	var details model.Material
	require.NoError(t, common.Unmarshal(response.Data, &details))
	expectedDetails := material
	expectedDetails.Username = owner.Username
	assert.Equal(t, expectedDetails, details)

	requestAdminMaterial(t, router, admin, http.MethodPut, fmt.Sprintf("%s/groups/%d", base, group.Id), `{"name":"Characters"}`, http.StatusOK, true)
	response = requestAdminMaterial(t, router, admin, http.MethodGet, path, "", http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &details))
	assert.Equal(t, "Characters", details.GroupName)
	response = requestAdminMaterial(t, router, admin, http.MethodPost, base+"/groups", `{"name":"Scenes"}`, http.StatusOK, true)
	var destination model.MaterialGroup
	require.NoError(t, common.Unmarshal(response.Data, &destination))
	for _, tc := range []struct {
		name string
		id   *int
	}{
		{"move", &destination.Id}, {"ungroup", nil}, {"regroup", &group.Id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{"group_id": tc.id})
			require.NoError(t, err)
			response := requestAdminMaterial(t, router, admin, http.MethodPut, path+"/group", string(body), http.StatusOK, true)
			var moved model.Material
			require.NoError(t, common.Unmarshal(response.Data, &moved))
			assert.Equal(t, tc.id, moved.GroupId)
			if tc.id == nil {
				assert.Empty(t, moved.GroupName)
			}
		})
	}

	var beforeMaterial model.Material
	var beforeGroup model.MaterialGroup
	require.NoError(t, db.First(&beforeMaterial, material.Id).Error)
	require.NoError(t, db.First(&beforeGroup, foreign.Id).Error)
	for _, tc := range []struct {
		name, method, target, body string
	}{
		{"foreign group move", http.MethodPut, path + "/group", fmt.Sprintf(`{"group_id":%d}`, foreign.Id)},
		{"missing group_id", http.MethodPut, path + "/group", `{}`},
		{"zero group", http.MethodPut, path + "/group", `{"group_id":0}`},
		{"foreign group rename", http.MethodPut, fmt.Sprintf("%s/groups/%d", base, foreign.Id), `{"name":"Stolen"}`},
		{"wrong owner details", http.MethodGet, fmt.Sprintf("%s/materials/%d", otherBase, material.Id), ""},
		{"wrong owner move", http.MethodPut, fmt.Sprintf("%s/materials/%d/group", otherBase, material.Id), `{"group_id":null}`},
		{"wrong owner delete", http.MethodDelete, fmt.Sprintf("%s/materials/%d", otherBase, material.Id), ""},
		{"negative group", http.MethodPut, path + "/group", `{"group_id":-1}`},
		{"string group", http.MethodPut, path + "/group", `{"group_id":"invalid"}`},
		{"foreign group create", http.MethodPost, base + "/materials", fmt.Sprintf(`{"name":"denied","file_type":"Image","source_url":%q,"group_id":%d}`, publicMaterialURL, foreign.Id)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestAdminMaterial(t, router, admin, tc.method, tc.target, tc.body, http.StatusOK, false)
			var afterMaterial model.Material
			var afterGroup model.MaterialGroup
			require.NoError(t, db.First(&afterMaterial, material.Id).Error)
			require.NoError(t, db.First(&afterGroup, foreign.Id).Error)
			assert.Equal(t, beforeMaterial, afterMaterial)
			assert.Equal(t, beforeGroup, afterGroup)
			var count int64
			require.NoError(t, db.Model(&model.Material{}).Where("user_id = ?", owner.Id).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}

	// The unchanged AdminAuth audit must attribute writes to the administrator.
	var audit model.AuditLog
	require.NoError(t, db.Where("user_id = ? AND category = ? AND route = ? AND success = ?", admin.Id, model.AuditCategoryOperation, "/api/material/admin/users/:user_id/materials", true).First(&audit).Error)
	assert.Equal(t, admin.Username, audit.Username)
	require.NotNil(t, audit.Other.AdminInfo)
	require.NotNil(t, audit.Other.AuditInfo)
	assert.Equal(t, admin.Id, audit.Other.AdminInfo.AdminID)
	assert.Equal(t, strconv.Itoa(owner.Id), audit.Other.AuditInfo.Params["user_id"])

	requestAdminMaterial(t, router, admin, http.MethodDelete, path, "", http.StatusOK, true)
	response = requestAdminMaterial(t, router, admin, http.MethodGet, path, "", http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &details))
	assert.True(t, details.Deleting)
	requestAdminMaterial(t, router, admin, http.MethodDelete, path, "", http.StatusOK, false)
	var remaining int64
	require.NoError(t, db.Model(&model.Material{}).Where("id = ?", material.Id).Count(&remaining).Error)
	assert.EqualValues(t, 1, remaining, "queued deletion retains the record until upstream confirmation")
}

func TestAdminMaterialMappingsAndQueuedDeletion(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Material{}, &model.MaterialGroup{}, &model.MaterialBinding{}))
	router := newAdminMaterialRouter(t)
	materials := []model.Material{
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "mapped first", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?image=first", Status: "ready"},
		{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "mapped second", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL + "?image=second", Status: "ready"},
	}
	require.NoError(t, db.Create(&materials).Error)
	bindings := []model.MaterialBinding{
		{MaterialId: materials[0].Id, Scope: "account-one", Version: model.MaterialSourceVersion(&materials[0]), ChannelID: 11, ChannelName: "First channel", Provider: "mobile_cloud", GroupID: "group-one", AssetID: "asset-first", State: "ready", UpstreamStatus: "ACTIVE"},
		{MaterialId: materials[0].Id, Scope: "account-two", Version: model.MaterialSourceVersion(&materials[0]), ChannelID: 12, ChannelName: "Second channel", Provider: "mobile_cloud", GroupID: "group-two", AssetID: "asset-second", State: "failed", UpstreamStatus: "FAILED"},
		{MaterialId: materials[1].Id, Scope: "account-two", Version: model.MaterialSourceVersion(&materials[1]), ChannelID: 12, ChannelName: "Second channel", Provider: "mobile_cloud", GroupID: "group-two", AssetID: "asset-other", State: "ready", UpstreamStatus: "ACTIVE"},
	}
	require.NoError(t, db.Create(&bindings).Error)
	for _, tc := range []struct {
		query string
		total int64
	}{
		{"source_url=" + url.QueryEscape(materials[0].SourceURL), 1},
		{"keyword=asset-first", 1},
		{"keyword=group-one", 1},
		{"channel_id=12&upstream_status=FAILED", 1},
		{"channel_id=11&upstream_status=FAILED", 0},
		{"keyword=asset-first&channel_id=12", 0},
	} {
		response := requestAdminMaterial(t, router, &users[0], http.MethodGet, fmt.Sprintf("/api/material/all?user_id=%d&%s", users[1].Id, tc.query), "", http.StatusOK, true)
		assert.Equal(t, tc.total, response.Total, tc.query)
	}
	base := fmt.Sprintf("/api/material/admin/users/%d/materials/%d", users[1].Id, materials[0].Id)
	response := requestAdminMaterial(t, router, &users[0], http.MethodGet, base, "", http.StatusOK, true)
	var detail model.Material
	require.NoError(t, common.Unmarshal(response.Data, &detail))
	require.Len(t, detail.Bindings, 2)
	assert.Equal(t, "asset-first", detail.Bindings[0].AssetID)
	assert.NotContains(t, string(response.Data), "account-one")
	assert.NotContains(t, string(response.Data), "locked_until")
	requestAdminMaterial(t, router, &users[0], http.MethodPut, base+"/group", `{"group_id":null}`, http.StatusOK, false)
	response = requestAdminMaterial(t, router, &users[0], http.MethodDelete, base, "", http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &detail))
	assert.True(t, detail.Deleting)
	assert.Equal(t, "processing", detail.Status)
	var count int64
	require.NoError(t, db.Model(&model.MaterialBinding{}).Where("material_id = ?", detail.Id).Count(&count).Error)
	assert.EqualValues(t, 2, count, "deletion must retain upstream mappings until confirmed")
	require.NoError(t, db.Model(&model.Material{}).Where("id = ?", detail.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	requestAdminMaterial(t, router, &users[0], http.MethodDelete, base, "", http.StatusOK, false)
	for _, query := range []string{"channel_id=-1", "channel_id=x", "upstream_status=invalid"} {
		requestAdminMaterial(t, router, &users[0], http.MethodGet, "/api/material/all?"+query, "", http.StatusBadRequest, false)
	}
	t.Run("confirmed deletion cannot turn a queued response into not found", func(t *testing.T) {
		immediate := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "immediate deletion", LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
		require.NoError(t, db.Create(&immediate).Error)
		require.NoError(t, db.Callback().Update().After("gorm:update").Register("material_test_completed_deletion", func(tx *gorm.DB) {
			values, ok := tx.Statement.Dest.(map[string]any)
			if tx.Statement.Table == "materials" && ok && values["deleting"] == true {
				tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Where("id = ?", immediate.Id).Delete(&model.Material{}).Error)
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("material_test_completed_deletion")) })
		path := fmt.Sprintf("/api/material/admin/users/%d/materials/%d", users[1].Id, immediate.Id)
		response := requestAdminMaterial(t, router, &users[0], http.MethodDelete, path, "", http.StatusOK, true)
		var queued model.Material
		require.NoError(t, common.Unmarshal(response.Data, &queued))
		assert.Equal(t, immediate.Id, queued.Id)
		assert.True(t, queued.Deleting)
	})
}

func TestAdminMaterialGroupQueue(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Material{}, &model.MaterialGroup{}, &model.MaterialGroupBinding{}))
	router := newAdminMaterialRouter(t)
	base := fmt.Sprintf("/api/material/admin/users/%d/groups", users[1].Id)
	response := requestAdminMaterial(t, router, &users[0], http.MethodPost, base, `{"name":"Synchronized portraits"}`, http.StatusOK, true)
	var group model.MaterialGroup
	require.NoError(t, common.Unmarshal(response.Data, &group))
	assert.Equal(t, "pending", group.Status, "a local row does not prove upstream success")
	binding := model.MaterialGroupBinding{GroupId: group.Id, UserId: users[1].Id, Scope: "private-account-scope", ChannelID: 11, ChannelName: "First channel", Provider: "mobile_cloud", Name: group.Name, UpstreamID: "remote-group-11", State: "ready"}
	require.NoError(t, db.Create(&binding).Error)
	path := fmt.Sprintf("%s/%d", base, group.Id)
	for _, operation := range []struct{ method, suffix, body string }{
		{http.MethodGet, "", ""},
		{http.MethodPut, "/" + strconv.Itoa(group.Id), `{"name":"Renamed portraits"}`},
		{http.MethodPost, "/" + strconv.Itoa(group.Id) + "/retry", ""},
	} {
		if operation.suffix != "" && operation.method == http.MethodPost {
			require.NoError(t, db.Model(&model.MaterialGroup{}).Where("id = ?", group.Id).Update("status", "failed").Error)
		}
		response = requestAdminMaterial(t, router, &users[0], operation.method, base+operation.suffix, operation.body, http.StatusOK, true)
		var raw map[string]any
		if operation.method == http.MethodGet {
			var groups []map[string]any
			require.NoError(t, common.Unmarshal(response.Data, &groups))
			require.Len(t, groups, 1)
			raw = groups[0]
		} else {
			require.NoError(t, common.Unmarshal(response.Data, &raw))
		}
		items, ok := raw["bindings"].([]any)
		require.True(t, ok, "group operation responses must retain upstream mappings")
		require.Len(t, items, 1)
		assert.Equal(t, "remote-group-11", items[0].(map[string]any)["upstream_id"])
		assert.NotContains(t, string(response.Data), "private-account-scope")
	}
	material := model.Material{MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: "portrait", GroupId: &group.Id, LibraryType: "AIGC", FileType: "Image", SourceURL: publicMaterialURL, Status: "pending"}
	require.NoError(t, db.Create(&material).Error)
	requestAdminMaterial(t, router, &users[0], http.MethodDelete, path, "", http.StatusOK, false)
	require.NoError(t, db.Delete(&material).Error)
	response = requestAdminMaterial(t, router, &users[0], http.MethodDelete, path, "", http.StatusOK, true)
	require.NoError(t, common.Unmarshal(response.Data, &group))
	assert.True(t, group.Deleting)
	assert.Equal(t, "processing", group.Status)
	require.Len(t, group.Bindings, 1)
	assert.Equal(t, "remote-group-11", group.Bindings[0].UpstreamID)
	requestAdminMaterial(t, router, &users[1], http.MethodDelete, path, "", http.StatusForbidden, false)
}

func TestAdminMaterialPermissionGate(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Material{}))
	router := newAdminMaterialRouter(t)
	base := fmt.Sprintf("/api/material/admin/users/%d", users[2].Id)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/material/all", ""},
		{http.MethodGet, "/api/material/list", ""},
		{http.MethodGet, "/api/material/groups", ""},
		{http.MethodPost, "/api/material/groups", `{"name":"denied"}`},
		{http.MethodPut, "/api/material/groups/1", `{"name":"denied"}`},
		{http.MethodPost, "/api/material/", `{"name":"denied"}`},
		{http.MethodGet, "/api/material/1", ""},
		{http.MethodDelete, "/api/material/1", ""},
		{http.MethodPost, "/api/material/1/retry", ""},
		{http.MethodPut, "/api/material/1/group", `{"group_id":null}`},
		{http.MethodPost, base + "/materials", `{"name":"denied"}`},
		{http.MethodGet, base + "/materials/1", ""},
		{http.MethodDelete, base + "/materials/1", ""},
		{http.MethodPost, base + "/materials/1/retry", ""},
		{http.MethodPut, base + "/materials/1/group", `{"group_id":null}`},
		{http.MethodGet, base + "/groups", ""},
		{http.MethodPost, base + "/groups", `{"name":"denied"}`},
		{http.MethodPut, base + "/groups/1", `{"name":"denied"}`},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			requestAdminMaterial(t, router, &users[1], tc.method, tc.path, tc.body, http.StatusForbidden, false)
			requestAdminMaterial(t, router, nil, tc.method, tc.path, tc.body, http.StatusUnauthorized, false)
		})
	}
	var count int64
	require.NoError(t, db.Model(&model.Material{}).Where("user_id IN ?", []int{users[1].Id, users[2].Id}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&model.MaterialGroup{}).Where("user_id IN ?", []int{users[1].Id, users[2].Id}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAdminMaterialInvalidOwner(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Material{}))
	router := newAdminMaterialRouter(t)
	// Soft-deleted users must not be valid management targets either.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", users[2].Id).Update("deleted_at", time.Now()).Error)
	var missing int
	require.NoError(t, db.Unscoped().Model(&model.User{}).Select("COALESCE(MAX(id), 0) + 1").Scan(&missing).Error)
	name := common.GetUUID()
	for _, owner := range []string{"0", "-1", "abc", "1.5", "999999999999999999999999", strconv.Itoa(missing), strconv.Itoa(users[2].Id)} {
		t.Run(owner, func(t *testing.T) {
			base := "/api/material/admin/users/" + owner
			for _, tc := range []struct{ method, path, body string }{
				{http.MethodPost, base + "/materials", fmt.Sprintf(`{"name":%q,"file_type":"Image","source_url":%q}`, name, publicMaterialURL)},
				{http.MethodGet, base + "/materials/1", ""},
				{http.MethodDelete, base + "/materials/1", ""},
				{http.MethodPost, base + "/materials/1/retry", ""},
				{http.MethodPut, base + "/materials/1/group", `{"group_id":null}`},
				{http.MethodGet, base + "/groups", ""},
				{http.MethodPost, base + "/groups", fmt.Sprintf(`{"name":%q}`, name)},
				{http.MethodPut, base + "/groups/1", fmt.Sprintf(`{"name":%q}`, name)},
			} {
				requestAdminMaterial(t, router, &users[0], tc.method, tc.path, tc.body, http.StatusBadRequest, false)
			}
			requestAdminMaterial(t, router, &users[0], http.MethodGet, "/api/material/all?user_id="+owner, "", http.StatusBadRequest, false)
		})
	}
	var count int64
	require.NoError(t, db.Model(&model.Material{}).Where("name = ?", name).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&model.MaterialGroup{}).Where("name = ?", name).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAdminMaterialCreateValidation(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	require.NoError(t, db.AutoMigrate(&model.Material{}))
	router := newAdminMaterialRouter(t)
	fetch := system_setting.GetFetchSetting()
	previousFetch := *fetch
	*fetch = system_setting.FetchSetting{EnableSSRFProtection: true, AllowedPorts: []string{"443"}, ApplyIPFilterForDomain: true}
	t.Cleanup(func() { *fetch = previousFetch })
	for _, tc := range []struct{ name, body string }{
		{"malformed JSON", `{"name":`},
		{"real person pending", `{"name":"face","library_type":"LivenessFace","file_type":"Image","source_url":"` + publicMaterialURL + `"}`},
		{"missing name", `{"file_type":"Image","source_url":"` + publicMaterialURL + `"}`},
		{"invalid file", `{"name":"x","file_type":"Doc","source_url":"` + publicMaterialURL + `"}`},
		{"video unsupported", `{"name":"x","file_type":"Video","source_url":"` + publicMaterialURL + `"}`},
		{"audio unsupported", `{"name":"x","file_type":"Audio","source_url":"` + publicMaterialURL + `"}`},
		{"missing URL", `{"name":"x","file_type":"Image"}`},
		{"HTTPS required", `{"name":"x","file_type":"Image","source_url":"http://8.8.8.8:443/x"}`},
		{"userinfo forbidden", `{"name":"x","file_type":"Image","source_url":"https://user:pass@8.8.8.8/x"}`},
		{"fragment forbidden", `{"name":"x","file_type":"Image","source_url":"https://8.8.8.8/x#fragment"}`},
		{"SSRF loopback", `{"name":"x","file_type":"Image","source_url":"https://127.0.0.1/x"}`},
		{"SSRF private", `{"name":"x","file_type":"Image","source_url":"https://10.0.0.1/x"}`},
		{"SSRF scheme", `{"name":"x","file_type":"Image","source_url":"file:///etc/passwd"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestAdminMaterial(t, router, &users[0], http.MethodPost, fmt.Sprintf("/api/material/admin/users/%d/materials", users[1].Id), tc.body, http.StatusOK, false)
			requestAdminMaterial(t, router, &users[1], http.MethodPost, "/api/material/", tc.body, http.StatusForbidden, false)
		})
	}
	var count int64
	require.NoError(t, db.Model(&model.Material{}).Where("user_id IN ?", []int{users[0].Id, users[1].Id}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAdminMaterialSelfOwnership(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for i := range users {
		users[i].Role = common.RoleAdminUser
		require.NoError(t, db.Model(&users[i]).Update("role", common.RoleAdminUser).Error)
	}
	router := newAdminMaterialRouter(t)
	admin, owner, other := &users[0], &users[1], &users[2]
	spoof := "?user_id=" + strconv.Itoa(other.Id)
	response := requestAdminMaterial(t, router, owner, http.MethodPost, "/api/material/groups"+spoof, fmt.Sprintf(`{"name":"Own","user_id":%d}`, other.Id), http.StatusOK, true)
	var ownGroup model.MaterialGroup
	require.NoError(t, common.Unmarshal(response.Data, &ownGroup))
	assert.Equal(t, owner.Id, ownGroup.UserId)
	response = requestAdminMaterial(t, router, other, http.MethodPost, "/api/material/groups", `{"name":"Foreign"}`, http.StatusOK, true)
	var foreignGroup model.MaterialGroup
	require.NoError(t, common.Unmarshal(response.Data, &foreignGroup))
	response = requestAdminMaterial(t, router, owner, http.MethodPost, "/api/material/"+spoof, fmt.Sprintf(`{"name":"own","file_type":"Image","source_url":%q,"group_id":%d,"user_id":%d}`, publicMaterialURL, ownGroup.Id, other.Id), http.StatusOK, true)
	var own model.Material
	require.NoError(t, common.Unmarshal(response.Data, &own))
	assert.Equal(t, owner.Id, own.UserId)
	assert.Equal(t, model.MaterialStatusPending, own.Status)
	response = requestAdminMaterial(t, router, other, http.MethodPost, "/api/material/", fmt.Sprintf(`{"name":"foreign","file_type":"Image","source_url":%q,"group_id":%d}`, publicMaterialURL, foreignGroup.Id), http.StatusOK, true)
	var foreign model.Material
	require.NoError(t, common.Unmarshal(response.Data, &foreign))
	response = requestAdminMaterial(t, router, owner, http.MethodGet, "/api/material/list"+spoof, "", http.StatusOK, true)
	var materials []model.Material
	require.NoError(t, common.Unmarshal(response.Data, &materials))
	assert.EqualValues(t, 1, response.Total)
	require.Len(t, materials, 1)
	assert.Equal(t, own.Id, materials[0].Id)
	response = requestAdminMaterial(t, router, owner, http.MethodGet, "/api/material/groups"+spoof, "", http.StatusOK, true)
	var groups []model.MaterialGroup
	require.NoError(t, common.Unmarshal(response.Data, &groups))
	require.Len(t, groups, 1)
	assert.Equal(t, ownGroup.Id, groups[0].Id)
	response = requestAdminMaterial(t, router, admin, http.MethodGet, "/api/material/list"+spoof, "", http.StatusOK, true)
	assert.Zero(t, response.Total, "even administrators' self lists are actor-scoped")

	var before model.Material
	var beforeGroup model.MaterialGroup
	require.NoError(t, db.First(&before, foreign.Id).Error)
	require.NoError(t, db.First(&beforeGroup, foreignGroup.Id).Error)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, fmt.Sprintf("/api/material/%d", foreign.Id) + spoof, ""},
		{http.MethodDelete, fmt.Sprintf("/api/material/%d", foreign.Id) + spoof, ""},
		{http.MethodPut, fmt.Sprintf("/api/material/%d/group", foreign.Id) + spoof, `{"group_id":null}`},
		{http.MethodPut, fmt.Sprintf("/api/material/%d/group", own.Id) + spoof, fmt.Sprintf(`{"group_id":%d}`, foreignGroup.Id)},
		{http.MethodPut, fmt.Sprintf("/api/material/groups/%d", foreignGroup.Id) + spoof, `{"name":"Stolen"}`},
	} {
		requestAdminMaterial(t, router, owner, tc.method, tc.path, tc.body, http.StatusOK, false)
	}
	requestAdminMaterial(t, router, admin, http.MethodGet, fmt.Sprintf("/api/material/%d", foreign.Id)+spoof, "", http.StatusOK, false)
	var after model.Material
	var afterGroup model.MaterialGroup
	require.NoError(t, db.First(&after, foreign.Id).Error)
	require.NoError(t, db.First(&afterGroup, foreignGroup.Id).Error)
	assert.Equal(t, before, after)
	assert.Equal(t, beforeGroup, afterGroup)
	response = requestAdminMaterial(t, router, owner, http.MethodGet, fmt.Sprintf("/api/material/%d", own.Id)+spoof, "", http.StatusOK, true)
	var details model.Material
	require.NoError(t, common.Unmarshal(response.Data, &details))
	assert.Equal(t, own.Id, details.Id)
	requestAdminMaterial(t, router, owner, http.MethodPut, fmt.Sprintf("/api/material/groups/%d", ownGroup.Id)+spoof, `{"name":"Renamed"}`, http.StatusOK, true)
	requestAdminMaterial(t, router, owner, http.MethodPut, fmt.Sprintf("/api/material/%d/group", own.Id)+spoof, `{"group_id":null}`, http.StatusOK, true)
	requestAdminMaterial(t, router, owner, http.MethodDelete, fmt.Sprintf("/api/material/%d", own.Id)+spoof, "", http.StatusOK, true)
	var count int64
	require.NoError(t, db.Model(&model.Material{}).Where("id = ? AND deleting = ?", own.Id, true).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAdminMaterialRetryOwnershipAndSuccess(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	for i := range users {
		users[i].Role = common.RoleAdminUser
		require.NoError(t, db.Model(&users[i]).Update("role", common.RoleAdminUser).Error)
	}
	require.NoError(t, db.AutoMigrate(&model.Material{}, &model.MaterialGroup{}))
	router := newAdminMaterialRouter(t)
	admin, owner, other := &users[0], &users[1], &users[2]
	group := model.MaterialGroup{UserId: owner.Id, Name: "Retry references"}
	require.NoError(t, model.SaveMaterialGroup(&group))
	for _, adminRoute := range []bool{false, true} {
		name := "self"
		if adminRoute {
			name = "admin"
		}
		t.Run(name, func(t *testing.T) {
			material := model.Material{
				MaterialID: model.NewPlatformMaterialID(), UserId: owner.Id, Name: "retry image", GroupId: &group.Id,
				LibraryType: model.MaterialLibraryAIGC, FileType: model.MaterialFileImage, SourceURL: publicMaterialURL,
				Status: model.MaterialStatusFailed, FailReason: "upstream processing failed", CanRetry: true, CreatedTime: 1, UpdatedTime: 1,
			}
			require.NoError(t, db.Create(&material).Error)
			actor := owner
			path := fmt.Sprintf("/api/material/%d/retry", material.Id)
			deniedActor := other
			deniedPath := path + "?user_id=" + strconv.Itoa(owner.Id)
			if adminRoute {
				actor = admin
				path = fmt.Sprintf("/api/material/admin/users/%d/materials/%d/retry", owner.Id, material.Id)
				deniedActor = admin
				deniedPath = fmt.Sprintf("/api/material/admin/users/%d/materials/%d/retry", other.Id, material.Id)
			}
			response := requestAdminMaterial(t, router, deniedActor, http.MethodPost, deniedPath, fmt.Sprintf(`{"user_id":%d}`, owner.Id), http.StatusOK, false)
			assert.Equal(t, "material not found", response.Message)
			if !adminRoute {
				requestAdminMaterial(t, router, nil, http.MethodPost, path, "", http.StatusUnauthorized, false)
				requestAdminMaterial(t, router, admin, http.MethodPost, path, "", http.StatusOK, false)
			}
			var unchanged model.Material
			require.NoError(t, db.First(&unchanged, material.Id).Error)
			assert.Equal(t, material, unchanged, "denied retries must not change another owner's material")

			response = requestAdminMaterial(t, router, actor, http.MethodPost, path+"?user_id="+strconv.Itoa(other.Id), fmt.Sprintf(`{"user_id":%d}`, other.Id), http.StatusOK, true)
			var retried model.Material
			require.NoError(t, common.Unmarshal(response.Data, &retried))
			assert.Equal(t, material.Id, retried.Id)
			assert.Equal(t, owner.Id, retried.UserId)
			assert.Equal(t, owner.Username, retried.Username)
			assert.Equal(t, group.Name, retried.GroupName)
			assert.Equal(t, material.MaterialID, retried.MaterialID)
			assert.Equal(t, material.SourceURL, retried.SourceURL)
			assert.Equal(t, material.CreatedTime, retried.CreatedTime)
			assert.Equal(t, model.MaterialStatusPending, retried.Status)
			assert.Empty(t, retried.FailReason)
			assert.False(t, retried.CanRetry)
			assert.Greater(t, retried.UpdatedTime, material.UpdatedTime)
			var stored model.Material
			require.NoError(t, db.First(&stored, material.Id).Error)
			assert.Equal(t, model.MaterialStatusPending, stored.Status)
			assert.Empty(t, stored.FailReason)
			assert.False(t, stored.CanRetry)
			requestAdminMaterial(t, router, actor, http.MethodPost, path, "", http.StatusOK, false)
			require.NoError(t, db.First(&unchanged, material.Id).Error)
			assert.Equal(t, stored, unchanged, "a repeated retry must not reset the pending material")
			if adminRoute {
				var audit model.AuditLog
				require.NoError(t, db.Where("user_id = ? AND category = ? AND route = ? AND success = ?", admin.Id, model.AuditCategoryOperation, "/api/material/admin/users/:user_id/materials/:id/retry", true).First(&audit).Error)
				require.NotNil(t, audit.Other.AdminInfo)
				require.NotNil(t, audit.Other.AuditInfo)
				assert.Equal(t, admin.Id, audit.Other.AdminInfo.AdminID)
				assert.Equal(t, strconv.Itoa(owner.Id), audit.Other.AuditInfo.Params["user_id"])
			}
		})
	}
}

func TestAdminMaterialRetryRejectsInvalidStateAndID(t *testing.T) {
	db, users := setupAdminMaterialPostgres(t)
	users[1].Role = common.RoleAdminUser
	require.NoError(t, db.Model(&users[1]).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, db.AutoMigrate(&model.Material{}))
	router := newAdminMaterialRouter(t)
	for _, tc := range []struct {
		name, status string
		canRetry     bool
	}{
		{"pending", model.MaterialStatusPending, true},
		{"processing", model.MaterialStatusProcessing, true},
		{"ready", model.MaterialStatusReady, true},
		{"permanent failure", model.MaterialStatusFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			material := model.Material{
				MaterialID: model.NewPlatformMaterialID(), UserId: users[1].Id, Name: tc.name,
				LibraryType: model.MaterialLibraryAIGC, FileType: model.MaterialFileImage, SourceURL: publicMaterialURL,
				Status: tc.status, FailReason: "retained reason", CanRetry: tc.canRetry, CreatedTime: 1, UpdatedTime: 1,
			}
			require.NoError(t, db.Create(&material).Error)
			for _, route := range []struct {
				actor *model.User
				path  string
			}{
				{&users[1], fmt.Sprintf("/api/material/%d/retry", material.Id)},
				{&users[0], fmt.Sprintf("/api/material/admin/users/%d/materials/%d/retry", users[1].Id, material.Id)},
			} {
				response := requestAdminMaterial(t, router, route.actor, http.MethodPost, route.path, "", http.StatusOK, false)
				assert.Equal(t, "material cannot be retried in its current state", response.Message)
				var stored model.Material
				require.NoError(t, db.First(&stored, material.Id).Error)
				assert.Equal(t, material, stored)
			}
		})
	}
	var missing int
	require.NoError(t, db.Model(&model.Material{}).Select("COALESCE(MAX(id), 0) + 1").Scan(&missing).Error)
	for _, id := range []string{"0", "-1", "abc", "999999999999999999999999", strconv.Itoa(missing)} {
		t.Run("invalid material "+id, func(t *testing.T) {
			status := http.StatusBadRequest
			message := "invalid material id"
			if id == strconv.Itoa(missing) {
				status = http.StatusOK
				message = "material not found"
			}
			response := requestAdminMaterial(t, router, &users[1], http.MethodPost, "/api/material/"+id+"/retry", "", status, false)
			assert.Equal(t, message, response.Message)
			response = requestAdminMaterial(t, router, &users[0], http.MethodPost, fmt.Sprintf("/api/material/admin/users/%d/materials/%s/retry", users[1].Id, id), "", status, false)
			assert.Equal(t, message, response.Message)
		})
	}
}
