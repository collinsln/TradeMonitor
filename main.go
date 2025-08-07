package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	_ "modernc.org/sqlite"
)

// Prometheus metrics for trading_orders table monitoring
var (
	totalOrders = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "trading_orders_total",
			Help: "Total number of trading orders by status and side",
		},
		[]string{"status", "side"},
	)

	ordersWithStopLoss = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_orders_with_stop_loss",
			Help: "Number of orders with stop loss configured",
		},
	)

	ordersWithTakeProfit = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_orders_with_take_profit",
			Help: "Number of orders with take profit configured",
		},
	)

	totalVolumeUSD = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "trading_orders_volume_usd",
			Help: "Total USD volume by status and side",
		},
		[]string{"status", "side"},
	)

	averageOrderSizeUSD = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_orders_avg_size_usd",
			Help: "Average order size in USD for filled orders",
		},
	)

	totalTradingFees = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_orders_total_fees_usd",
			Help: "Total trading fees paid in USD",
		},
	)

	recentFilledOrders = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_orders_recent_filled",
			Help: "Number of orders filled in the last hour",
		},
	)

	averageFillPrice = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "trading_orders_avg_fill_price",
			Help: "Average fill price by symbol and side",
		},
		[]string{"symbol", "side"},
	)

	dbConnectionStatus = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_orders_db_status",
			Help: "Database connection status (1=up, 0=down)",
		},
	)

	lastUpdateTime = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "trading_orders_last_update_timestamp",
			Help: "Unix timestamp of last metrics update",
		},
	)
)

func init() {
	// Register all trading_orders metrics with Prometheus
	prometheus.MustRegister(totalOrders)
	prometheus.MustRegister(ordersWithStopLoss)
	prometheus.MustRegister(ordersWithTakeProfit)
	prometheus.MustRegister(totalVolumeUSD)
	prometheus.MustRegister(averageOrderSizeUSD)
	prometheus.MustRegister(totalTradingFees)
	prometheus.MustRegister(recentFilledOrders)
	prometheus.MustRegister(averageFillPrice)
	prometheus.MustRegister(dbConnectionStatus)
	prometheus.MustRegister(lastUpdateTime)
}

func main() {
	// Look for database file in current directory first, then parent directories
	dbPaths := []string{
		"grid_trading.db",       // Current directory
		"../grid_trading.db",    // Parent directory
		"../../grid_trading.db", // Grandparent directory
	}

	var dbPath string
	var found bool

	for _, path := range dbPaths {
		if absPath, err := filepath.Abs(path); err == nil {
			if _, err := os.Stat(absPath); err == nil {
				dbPath = absPath
				found = true
				break
			}
		}
	}

	if !found {
		log.Fatal("❌ Could not find grid_trading.db in current or parent directories")
	}

	fmt.Printf("🔗 Connecting to database: %s\n", dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal("❌ Failed to open SQLite database:", err)
	}
	defer db.Close()

	// Test connection and inspect database structure
	if err := inspectDatabase(db); err != nil {
		log.Printf("⚠️ Database inspection failed: %v", err)
	}

	// Start metrics collection in background
	go collectMetrics(db)

	// HTTP server for Prometheus scraping
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/health", healthHandler)

	fmt.Println("\n🚀 Trading Orders Metrics Exporter starting on :8080")
	fmt.Println("📊 Metrics endpoint: http://localhost:8080/metrics")
	fmt.Println("🌐 Web interface: http://localhost:8080")
	fmt.Println("❤️ Health check: http://localhost:8080/health")
	fmt.Println("\n✅ Server is running... Press Ctrl+C to stop")

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func inspectDatabase(db *sql.DB) error {
	// Test database connection
	if err := db.Ping(); err != nil {
		return fmt.Errorf("database ping failed: %v", err)
	}

	fmt.Println("✅ Database connection successful!")

	// Get list of tables
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Println("\n📋 Found tables in database:")
	tableFound := false
	for rows.Next() {
		var tableName string
		rows.Scan(&tableName)
		fmt.Printf("   • %s\n", tableName)

		// Get table schema for trading_orders specifically
		if tableName == "trading_orders" {
			tableFound = true
			schemaRows, err := db.Query("PRAGMA table_info(trading_orders)")
			if err != nil {
				continue
			}

			fmt.Printf("     📝 Columns in trading_orders:\n")
			for schemaRows.Next() {
				var cid int
				var name, dataType string
				var notNull, pk int
				var dfltValue sql.NullString

				schemaRows.Scan(&cid, &name, &dataType, &notNull, &dfltValue, &pk)
				fmt.Printf("        - %s (%s)\n", name, dataType)
			}
			schemaRows.Close()

			// Count total orders
			var totalCount int
			db.QueryRow("SELECT COUNT(*) FROM trading_orders").Scan(&totalCount)
			fmt.Printf("     📊 Total orders in table: %d\n", totalCount)
		}
	}

	if !tableFound {
		fmt.Println("⚠️ WARNING: trading_orders table not found!")
	}

	return nil
}

func collectMetrics(db *sql.DB) {
	fmt.Println("🔄 Starting metrics collection (updates every 30 seconds)...")

	for {
		// Test database connection
		if err := db.Ping(); err != nil {
			dbConnectionStatus.Set(0)
			log.Printf("❌ Database ping failed: %v", err)
		} else {
			dbConnectionStatus.Set(1)

			// Update trading metrics
			updateTradingMetrics(db)

			// Update timestamp
			lastUpdateTime.Set(float64(time.Now().Unix()))

			fmt.Printf("📈 Metrics updated at %s\n", time.Now().Format("15:04:05"))
		}

		// Update metrics every 30 seconds
		time.Sleep(30 * time.Second)
	}
}

func updateTradingMetrics(db *sql.DB) {
	// Update all trading_orders metrics
	updateOrdersByStatus(db)
	updateVolumeMetrics(db)
	updateRiskManagementMetrics(db)
	updateFeeMetrics(db)
	updateFilledOrdersMetrics(db)
	updatePriceMetrics(db)
}

func updateOrdersByStatus(db *sql.DB) {
	// Count orders by status and side from trading_orders table
	statusQuery := `
        SELECT 
            COALESCE(status, 'unknown') as status, 
            COALESCE(side, 'unknown') as side, 
            COUNT(*) as count,
            COALESCE(SUM(usd_amount), 0) as volume
        FROM trading_orders 
        GROUP BY status, side
    `

	rows, err := db.Query(statusQuery)
	if err != nil {
		log.Printf("Failed to query orders by status: %v", err)
		return
	}
	defer rows.Close()

	// Reset metrics to 0 first
	statuses := []string{"pending", "filled", "cancelled", "rejected", "partial"}
	sides := []string{"buy", "sell"}
	for _, status := range statuses {
		for _, side := range sides {
			totalOrders.WithLabelValues(status, side).Set(0)
			totalVolumeUSD.WithLabelValues(status, side).Set(0)
		}
	}

	// Update with actual values
	for rows.Next() {
		var status, side string
		var count, volume float64
		if err := rows.Scan(&status, &side, &count, &volume); err != nil {
			log.Printf("Failed to scan status row: %v", err)
			continue
		}
		totalOrders.WithLabelValues(status, side).Set(count)
		totalVolumeUSD.WithLabelValues(status, side).Set(volume)
	}
}

func updateVolumeMetrics(db *sql.DB) {
	// Calculate average order size for filled orders
	avgSizeQuery := `
        SELECT COALESCE(AVG(usd_amount), 0) 
        FROM trading_orders 
        WHERE status = 'filled' AND usd_amount > 0
    `

	var avgSize sql.NullFloat64
	if err := db.QueryRow(avgSizeQuery).Scan(&avgSize); err != nil {
		log.Printf("Failed to query average order size: %v", err)
		return
	}

	if avgSize.Valid {
		averageOrderSizeUSD.Set(avgSize.Float64)
	}
}

func updateRiskManagementMetrics(db *sql.DB) {
	// Count orders with risk management features
	riskQuery := `
        SELECT 
            COUNT(CASE WHEN stop_loss_price IS NOT NULL AND stop_loss_price > 0 THEN 1 END) as with_stop_loss,
            COUNT(CASE WHEN take_profit_price IS NOT NULL AND take_profit_price > 0 THEN 1 END) as with_take_profit
        FROM trading_orders
    `

	var withStopLoss, withTakeProfit sql.NullFloat64
	err := db.QueryRow(riskQuery).Scan(&withStopLoss, &withTakeProfit)
	if err != nil {
		log.Printf("Failed to query risk management metrics: %v", err)
		return
	}

	if withStopLoss.Valid {
		ordersWithStopLoss.Set(withStopLoss.Float64)
	}
	if withTakeProfit.Valid {
		ordersWithTakeProfit.Set(withTakeProfit.Float64)
	}
}

func updateFeeMetrics(db *sql.DB) {
	// Calculate total trading fees
	feeQuery := `
        SELECT COALESCE(SUM(trading_fee), 0) 
        FROM trading_orders 
        WHERE status = 'filled' AND trading_fee IS NOT NULL
    `

	var totalFees sql.NullFloat64
	if err := db.QueryRow(feeQuery).Scan(&totalFees); err != nil {
		log.Printf("Failed to query trading fees: %v", err)
		return
	}

	if totalFees.Valid {
		totalTradingFees.Set(totalFees.Float64)
	}
}

func updateFilledOrdersMetrics(db *sql.DB) {
	// Get recent filled orders (last hour)
	recentQuery := `
        SELECT COUNT(*)
        FROM trading_orders 
        WHERE status = 'filled' 
        AND filled_time IS NOT NULL 
        AND datetime(filled_time) > datetime('now', '-1 hour')
    `

	var recentCount sql.NullFloat64
	if err := db.QueryRow(recentQuery).Scan(&recentCount); err != nil {
		log.Printf("Failed to query recent filled orders: %v", err)
		return
	}

	if recentCount.Valid {
		recentFilledOrders.Set(recentCount.Float64)
	}
}

func updatePriceMetrics(db *sql.DB) {
	// Get average fill price by symbol and side
	priceQuery := `
        SELECT symbol, side, COALESCE(AVG(price), 0)
        FROM trading_orders 
        WHERE status = 'filled' AND price > 0
        GROUP BY symbol, side
    `

	rows, err := db.Query(priceQuery)
	if err != nil {
		log.Printf("Failed to query price metrics: %v", err)
		return
	}
	defer rows.Close()

	// Reset previous metrics
	averageFillPrice.Reset()

	for rows.Next() {
		var symbol, side string
		var avgPrice float64
		if err := rows.Scan(&symbol, &side, &avgPrice); err != nil {
			log.Printf("Failed to scan price row: %v", err)
			continue
		}
		averageFillPrice.WithLabelValues(symbol, side).Set(avgPrice)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	html := `
<!DOCTYPE html>
<html>
<head>
    <title>Trading Orders Metrics Exporter</title>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); min-height: 100vh; }
        .container { max-width: 1000px; margin: 0 auto; padding: 40px 20px; }
        .header { background: rgba(255,255,255,0.95); backdrop-filter: blur(10px); border-radius: 20px; padding: 40px; margin-bottom: 30px; box-shadow: 0 8px 32px rgba(0,0,0,0.1); }
        h1 { color: #2c3e50; font-size: 2.5em; margin-bottom: 15px; text-align: center; }
        .subtitle { color: #7f8c8d; text-align: center; font-size: 1.1em; margin-bottom: 30px; }
        .status { background: #2ecc71; color: white; padding: 8px 20px; border-radius: 25px; display: inline-block; font-weight: 600; margin-bottom: 20px; }
        
        .nav-links { display: flex; justify-content: center; gap: 20px; flex-wrap: wrap; }
        .nav-links a { display: inline-flex; align-items: center; padding: 12px 24px; background: linear-gradient(45deg, #3498db, #2980b9); color: white; text-decoration: none; border-radius: 10px; font-weight: 600; transition: all 0.3s ease; box-shadow: 0 4px 15px rgba(52, 152, 219, 0.3); }
        .nav-links a:hover { transform: translateY(-2px); box-shadow: 0 6px 20px rgba(52, 152, 219, 0.4); }
        
        .metrics-section { background: rgba(255,255,255,0.95); backdrop-filter: blur(10px); border-radius: 20px; padding: 30px; margin-bottom: 30px; box-shadow: 0 8px 32px rgba(0,0,0,0.1); }
        .metrics-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 20px; margin-top: 20px; }
        .metric-card { background: #f8f9fa; border-radius: 10px; padding: 20px; border-left: 4px solid #3498db; }
        .metric-name { font-family: 'Courier New', monospace; font-weight: bold; color: #2980b9; font-size: 0.9em; margin-bottom: 8px; }
        .metric-desc { color: #6c757d; font-size: 0.9em; line-height: 1.4; }
        
        .info-section { background: rgba(255,255,255,0.95); backdrop-filter: blur(10px); border-radius: 20px; padding: 30px; box-shadow: 0 8px 32px rgba(0,0,0,0.1); }
        .code-block { background: #2c3e50; color: #ecf0f1; padding: 20px; border-radius: 10px; overflow-x: auto; margin: 15px 0; font-family: 'Courier New', monospace; }
        .feature-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; margin: 20px 0; }
        .feature-item { background: #e8f5e8; padding: 12px; border-radius: 8px; border-left: 3px solid #27ae60; }
        
        h2 { color: #2c3e50; margin: 30px 0 20px 0; font-size: 1.8em; }
        h3 { color: #34495e; margin: 20px 0 10px 0; }
        
        @media (max-width: 768px) {
            .container { padding: 20px 10px; }
            h1 { font-size: 2em; }
            .nav-links { flex-direction: column; align-items: center; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🚀 Trading Orders Metrics Exporter</h1>
            <p class="subtitle">Real-time monitoring of your SQLite trading database</p>
            <div style="text-align: center;">
                <span class="status">✅ ACTIVE & MONITORING</span>
            </div>
            
            <div class="nav-links">
                <a href="/metrics">📊 Prometheus Metrics</a>
                <a href="/health">❤️ Health Check</a>
            </div>
        </div>

        <div class="metrics-section">
            <h2>📈 Available Metrics</h2>
            <div class="metrics-grid">
                <div class="metric-card">
                    <div class="metric-name">trading_orders_total{status,side}</div>
                    <div class="metric-desc">Count of orders by status (pending/filled/cancelled) and side (buy/sell)</div>
                </div>
                
                <div class="metric-card">
                    <div class="metric-name">trading_orders_volume_usd{status,side}</div>
                    <div class="metric-desc">Total USD volume by status and side</div>
                </div>
                
                <div class="metric-card">
                    <div class="metric-name">trading_orders_avg_size_usd</div>
                    <div class="metric-desc">Average order size in USD for filled orders</div>
                </div>
                
                <div class="metric-card">
                    <div class="metric-name">trading_orders_with_stop_loss</div>
                    <div class="metric-desc">Number of orders with stop loss configured</div>
                </div>
                
                <div class="metric-card">
                    <div class="metric-name">trading_orders_with_take_profit</div>
                    <div class="metric-desc">Number of orders with take profit configured</div>
                </div>
                
                <div class="metric-card">
                    <div class="metric-name">trading_orders_total_fees_usd</div>
                    <div class="metric-desc">Total trading fees paid in USD</div>
                </div>
                
                <div class="metric-card">
                    <div class="metric-name">trading_orders_recent_filled</div>
                    <div class="metric-desc">Number of orders filled in the last hour</div>
                </div>
                
                <div class="metric-card">
                    <div class="metric-name">trading_orders_avg_fill_price{symbol,side}</div>
                    <div class="metric-desc">Average fill price by trading symbol and side</div>
                </div>
            </div>
        </div>

        <div class="info-section">
            <h2>🗄️ Database Information</h2>
            <div class="feature-list">
                <div class="feature-item"><strong>Table:</strong> trading_orders</div>
                <div class="feature-item"><strong>Update Frequency:</strong> 30 seconds</div>
                <div class="feature-item"><strong>Connection:</strong> SQLite (Pure Go)</div>
                <div class="feature-item"><strong>Port:</strong> 8080</div>
            </div>
            
            <h3>📋 Table Columns:</h3>
            <p>order_id, symbol, side, price, quantity, usd_amount, status, stop_loss_price, take_profit_price, filled_time, trading_fee</p>
            
            <h2>⚙️ Prometheus Configuration</h2>
            <p>Add this to your <code>prometheus.yml</code> file:</p>
            <div class="code-block">scrape_configs:
  - job_name: 'trading-orders'
    static_configs:
      - targets: ['localhost:8080']
    scrape_interval: 30s</div>

            <h2>📊 Grafana Integration</h2>
            <p>Import metrics using data source: <code>http://localhost:8080/metrics</code></p>
            
            <div style="margin-top: 40px; text-align: center; color: #7f8c8d; font-size: 0.9em; border-top: 1px solid #ecf0f1; padding-top: 20px;">
                Built with Go + Prometheus • Moringa AI Capstone Project 2025
            </div>
        </div>
    </div>
</body>
</html>
`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{
        "status": "healthy",
        "service": "trading-orders-metrics-exporter",
        "version": "1.0.0",
        "database": "connected",
        "uptime": "running",
        "metrics_endpoint": "/metrics"
    }`))
}
