// Copyright 2012, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tabletserver

import (
	"fmt"
	"sync"
	"time"

	"github.com/juju/errors"
	"github.com/ngaut/cache"
	log "github.com/ngaut/logging"
	"github.com/wandoulabs/cm/mysql"
	"github.com/wandoulabs/cm/sqltypes"
	"github.com/wandoulabs/cm/vt/schema"
	"github.com/wandoulabs/cm/vt/tabletserver/planbuilder"
)

const base_show_tables = "select table_name, table_type, unix_timestamp(create_time), table_comment from information_schema.tables where table_schema = database()"

const maxTableCount = 10000

type ExecPlan struct {
	*planbuilder.ExecPlan
	TableInfo  *TableInfo
	mu         sync.Mutex
	QueryCount int64
	Time       time.Duration
	RowCount   int64
	ErrorCount int64
}

func (*ExecPlan) Size() int {
	return 1
}

func (ep *ExecPlan) AddStats(queryCount int64, duration time.Duration, rowCount, errorCount int64) {
	ep.mu.Lock()
	ep.QueryCount += queryCount
	ep.Time += duration
	ep.RowCount += rowCount
	ep.ErrorCount += errorCount
	ep.mu.Unlock()
}

func (ep *ExecPlan) Stats() (queryCount int64, duration time.Duration, rowCount, errorCount int64) {
	ep.mu.Lock()
	queryCount = ep.QueryCount
	duration = ep.Time
	rowCount = ep.RowCount
	errorCount = ep.ErrorCount
	ep.mu.Unlock()
	return
}

type OverrideCacheDesc struct {
	Type   string
	Prefix string
	Table  string
}

type SchemaOverride struct {
	Name      string
	PKColumns []string
	Cache     *OverrideCacheDesc
}

type SchemaInfo struct {
	tables     map[string]*TableInfo
	overrides  []SchemaOverride
	queries    *cache.LRUCache
	cachePool  *CachePool
	connPool   *mysql.DB
	lastChange time.Time
}

func NewSchemaInfo(rowCacheConf RowCacheConfig, dbAddr string, user, pwd, dbName string, overrides []SchemaOverride) *SchemaInfo {
	si := &SchemaInfo{
		queries:   cache.NewLRUCache(128 * 1024 * 1024),
		tables:    make(map[string]*TableInfo),
		cachePool: NewCachePool(dbName, rowCacheConf, 3*time.Second, 3*time.Second),
	}

	var err error
	si.connPool, err = mysql.Open(dbAddr, user, pwd, dbName)
	if err != nil { //todo: return error
		log.Fatal(err)
	}

	si.overrides = overrides
	si.connPool.SetMaxIdleConnNum(100)
	log.Infof("%+v", si.overrides)
	si.cachePool.Open()

	for _, or := range si.overrides {
		si.CreateOrUpdateTable(or.Name)
	}

	si.override()

	return si
}

func (si *SchemaInfo) override() {
	for _, override := range si.overrides {
		table, ok := si.tables[override.Name]
		if !ok {
			log.Warningf("Table not found for override: %v, %v", override, si.tables)
			continue
		}
		if override.PKColumns != nil {
			log.Infof("SetPK Table name %s, pk %v", override.Name, override.PKColumns)
			if err := table.SetPK(override.PKColumns); err != nil {
				log.Errorf("%s: %v", errors.ErrorStack(err), override)
				continue
			}
		}
		if si.cachePool.IsClosed() || override.Cache == nil {
			log.Infof("%+v", override)
			continue
		}

		switch override.Cache.Type {
		case "RW":
			table.CacheType = schema.CACHE_RW
			table.Cache = NewRowCache(table, si.cachePool)
		case "W":
			table.CacheType = schema.CACHE_W
			if len(override.Cache.Table) == 0 {
				log.Warningf("Incomplete cache specs: %v", override)
				continue
			}

			totable, ok := si.tables[override.Cache.Table]
			if !ok {
				log.Warningf("Table not found: %v", override)
				continue
			}

			if totable.Cache == nil {
				log.Warningf("Table has no cache: %v", override)
				continue
			}

			table.Cache = totable.Cache
		default:
			log.Warningf("Ignoring cache override: %+v", override)
		}
	}
}

func (si *SchemaInfo) Close() {
	si.tables = nil
	si.overrides = nil
	si.queries.Clear()
	si.cachePool.Close()
	si.connPool.Close()
}

func (si *SchemaInfo) Exec(sql string) (result *mysql.Result, err error) {
	conn, err := si.connPool.PopConn()
	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		si.connPool.PushConn(conn, err)
	}()

	result, err = conn.Execute(sql)

	return result, err
}

func (si *SchemaInfo) CreateOrUpdateTable(tableName string) {
	conn, err := si.connPool.PopConn()
	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		si.connPool.PushConn(conn, err)
	}()

	tables, err := conn.Execute(fmt.Sprintf("%s and table_name = '%s'", base_show_tables, tableName))
	if err != nil {
		log.Fatalf("Error fetching table %s: %v", tableName, err)
	}
	/*
		if len(tables.Rows) != 1 {
			// This can happen if DDLs race with each other.
			return
		}
	*/

	if len(tables.Values) == 0 { //table not exist
		log.Warningf("table %s not exist", tableName)
		return
	}

	create_time, err := sqltypes.BuildValue(tables.Values[0][2]) // create_time
	if err != nil {
		log.Error(err)
		return
	}

	tableInfo, err := NewTableInfo(
		conn,
		tableName,
		string(tables.Values[0][1].([]byte)), // table_type
		create_time,
		string(tables.Values[0][3].([]byte)), // table_comment
		si.cachePool,
	)
	if err != nil {
		// This can happen if DDLs race with each other.
		log.Error(err)
		return
	}

	if _, ok := si.tables[tableName]; ok {
		// If the table already exists, we overwrite it with the latest info.
		// This also means that the query cache needs to be cleared.
		// Otherwise, the query plans may not be in sync with the schema.
		si.queries.Clear()
		log.Infof("Updating table %s", tableName)
	}
	si.tables[tableName] = tableInfo

	if tableInfo.CacheType == schema.CACHE_NONE {
		log.Infof("Initialized table: %s", tableName)
	} else {
		log.Infof("Initialized cached table: %s, prefix: %s", tableName, tableInfo.Cache.prefix)
	}
}

func (si *SchemaInfo) DropTable(tableName string) {
	delete(si.tables, tableName)
	si.queries.Clear()
	log.Infof("Table %s forgotten", tableName)
}

func (si *SchemaInfo) GetTable(tableName string) *TableInfo {
	ti := si.tables[tableName]
	return ti
}

func (si *SchemaInfo) GetSchema() []*schema.Table {
	tables := make([]*schema.Table, 0, len(si.tables))
	for _, v := range si.tables {
		tables = append(tables, v.Table)
	}

	return tables
}

func (si *SchemaInfo) getQuery(sql string) *ExecPlan {
	if cacheResult, ok := si.queries.Get(sql); ok {
		return cacheResult.(*ExecPlan)
	}

	return nil
}

func (si *SchemaInfo) SetQueryCacheSize(size int) {
	if size <= 0 {
		log.Fatalf("cache size %v out of range", size)
	}
	si.queries.SetCapacity(int64(size))
}

func (si *SchemaInfo) getTableStats() map[string]int64 {
	tstats := make(map[string]int64)
	for k, v := range si.tables {
		if v.CacheType != schema.CACHE_NONE {
			hits, absent, misses, _ := v.Stats()
			tstats[k+".Hits"] = hits
			tstats[k+".Absent"] = absent
			tstats[k+".Misses"] = misses
		}
	}
	return tstats
}

func (si *SchemaInfo) getTableInvalidations() map[string]int64 {
	tstats := make(map[string]int64)
	for k, v := range si.tables {
		if v.CacheType != schema.CACHE_NONE {
			_, _, _, invalidations := v.Stats()
			tstats[k] = invalidations
		}
	}
	return tstats
}

func (si *SchemaInfo) getQueryCount() map[string]int64 {
	f := func(plan *ExecPlan) int64 {
		queryCount, _, _, _ := plan.Stats()
		return queryCount
	}
	return si.getQueryStats(f)
}

func (si *SchemaInfo) getQueryTime() map[string]int64 {
	f := func(plan *ExecPlan) int64 {
		_, time, _, _ := plan.Stats()
		return int64(time)
	}
	return si.getQueryStats(f)
}

func (si *SchemaInfo) getQueryRowCount() map[string]int64 {
	f := func(plan *ExecPlan) int64 {
		_, _, rowCount, _ := plan.Stats()
		return rowCount
	}
	return si.getQueryStats(f)
}

func (si *SchemaInfo) getQueryErrorCount() map[string]int64 {
	f := func(plan *ExecPlan) int64 {
		_, _, _, errorCount := plan.Stats()
		return errorCount
	}
	return si.getQueryStats(f)
}

type queryStatsFunc func(*ExecPlan) int64

func (si *SchemaInfo) getQueryStats(f queryStatsFunc) map[string]int64 {
	keys := si.queries.Keys()
	qstats := make(map[string]int64)
	for _, v := range keys {
		if plan := si.getQuery(v); plan != nil {
			table := plan.TableName
			if table == "" {
				table = "Join"
			}
			planType := plan.PlanId.String()
			data := f(plan)
			qstats[table+"."+planType] += data
		}
	}
	return qstats
}

type perQueryStats struct {
	Query      string
	Table      string
	Plan       planbuilder.PlanType
	QueryCount int64
	Time       time.Duration
	RowCount   int64
	ErrorCount int64
}
