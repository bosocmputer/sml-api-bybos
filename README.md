# sml-api-bybos

REST API สำหรับเชื่อมต่อกับฐานข้อมูล SML ERP (PostgreSQL) โดยตรง  
ทดแทน `SMLJavaRESTService3` (Java) ด้วย Go — เร็วกว่า, เรียบง่ายกว่า, ไม่ต้องติดตั้ง Java runtime

---

## สถาปัตยกรรม

```
billflow / openclaw / client app
         │
         ▼  HTTP REST (JSON)
sml-api-bybos  :8200
         │
         ▼  pgx/v5 connection pool
SML PostgreSQL  192.168.2.248:5432
```

- **ภาษา**: Go 1.24
- **Framework**: Gin
- **Driver**: pgx/v5 (connection pool per tenant)
- **Auth**: API Key via header
- **Multi-tenant**: tenant เลือกผ่าน header (database name)

---

## Quick Start

```bash
cp .env.example .env
# แก้ไข .env ตามความเหมาะสม
go run ./cmd/server
```

หรือใช้ Docker:

```bash
docker compose up -d
```

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8200` | TCP port |
| `HOST` | `0.0.0.0` | Bind address |
| `API_KEYS` | *(required)* | Comma-separated valid API keys เช่น `smlx,dev-key` |
| `DB_HOST` | `192.168.2.248` | SML PostgreSQL host |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_USER` | `postgres` | Database user |
| `DB_PASSWORD` | *(required)* | Database password |
| `DB_DEFAULT_TENANT` | `sml1_2026` | Tenant ที่ใช้ถ้าไม่ระบุ header |
| `DB_ALLOWED_TENANTS` | *(required)* | Comma-separated tenant ที่อนุญาต เช่น `sml1,sml1_2026,stp1` |
| `DB_MAX_CONNS` | `10` | Max connections per pool |
| `DB_MAX_IDLE_CONNS` | `5` | Max idle connections |

---

## Authentication

ทุก endpoint ภายใต้ `/api/v1/` ต้องส่ง API key ผ่าน header ใดหนึ่ง:

```
X-Api-Key: smlx
# หรือ (backward compat)
guid: smlx
```

---

## Multi-Tenant

ระบุ database (tenant) ผ่าน header ใดหนึ่ง:

```
X-Tenant: sml1_2026
# หรือ (backward compat)
databaseName: SML1_2026
```

> Header value ไม่ case-sensitive — `SML1_2026` และ `sml1_2026` ใช้ได้เหมือนกัน  
> ถ้าไม่ส่ง header จะใช้ `DB_DEFAULT_TENANT`

**Tenants ที่รองรับ**: `sml1`, `sml1_2026`, `stp1`, `datacenter_happy`

---

## Swagger UI

เปิด browser ไปที่:

```
http://localhost:8200/docs
```

หรือดู OpenAPI spec โดยตรง:

```
http://localhost:8200/docs/openapi.json
```

---

## Endpoints

### Health

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Liveness check |
| `GET` | `/health/ready` | Readiness check (ตรวจ DB connection) |

### Inventory / Products (`ic`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ic/products` | รายการสินค้าทั้งหมด (paginated) |
| `GET` | `/api/v1/ic/products/:code` | สินค้าตาม item code |
| `POST` | `/api/v1/ic/products` | สร้างสินค้าใหม่ใน SML |

### Sale Orders / Invoices / Purchase Orders

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/ic/sale-orders` | สร้างใบสั่งขาย (sale order) |
| `POST` | `/api/v1/ic/sale-invoices` | สร้างใบกำกับภาษี (sale invoice) |
| `POST` | `/api/v1/ic/purchase-orders` | สร้างใบสั่งซื้อ (purchase order) |

### Transactions

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ic/transactions` | รายการ transaction (paginated, filter ได้) |
| `GET` | `/api/v1/ic/transactions/summary` | สรุปยอดรายวัน |
| `GET` | `/api/v1/ic/transactions/:doc_no` | Transaction ตาม doc_no |
| `POST` | `/api/v1/ic/transactions` | สร้าง transaction ทั่วไป |

### Stock

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ic/stock` | ยอดคงเหลือสต๊อกทั้งหมด (paginated) |
| `GET` | `/api/v1/ic/stock/:code` | ยอดคงเหลือสต๊อกตาม item code |

### Warehouses

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ic/warehouses` | รายการคลังสินค้า |
| `GET` | `/api/v1/ic/warehouses/:code` | คลังสินค้าตาม warehouse code (รวม shelves) |

### Accounts Receivable / Customers (`ar`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ar/customers` | รายการลูกค้า (paginated) |
| `GET` | `/api/v1/ar/customers/:code` | ลูกค้าตาม customer code |

### Accounts Payable / Suppliers (`ap`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ap/suppliers` | รายการ supplier (paginated) |
| `GET` | `/api/v1/ap/suppliers/:code` | Supplier ตาม supplier code |

---

## Query Parameters

### Pagination (GET list endpoints)

| Parameter | Default | Description |
|---|---|---|
| `page` | `1` | หน้าที่ต้องการ |
| `size` | `100` | จำนวนรายการต่อหน้า (max 500) |

### Transaction Filters (`GET /api/v1/ic/transactions`)

| Parameter | Description |
|---|---|
| `from` | วันเริ่มต้น (YYYY-MM-DD) |
| `to` | วันสิ้นสุด (YYYY-MM-DD) |
| `type` | trans_flag เช่น `SO`, `SI`, `PO` |

---

## Request / Response Examples

### POST `/api/v1/ic/sale-orders`

```bash
curl -X POST http://localhost:8200/api/v1/ic/sale-orders \
  -H "guid: smlx" \
  -H "databaseName: SML1_2026" \
  -H "Content-Type: application/json" \
  -d '{
    "doc_no": "BF-SO2604000001",
    "doc_format_code": "SR",
    "doc_date": "2026-04-28",
    "cust_code": "AR00004",
    "sale_type": 0,
    "is_permium": 0,
    "vat_type": 0,
    "items": [
      {
        "item_code": "CON-01000",
        "unit_code": "ถุง",
        "qty": 10,
        "wh_code": "WH-01",
        "shelf_code": "SH-01",
        "price_exclude_vat": 250.00,
        "sum_amount_exclude_vat": 2500.00
      }
    ]
  }'
```

**Response:**
```json
{
  "success": true,
  "data": { "doc_no": "BF-SO2604000001" }
}
```

### GET `/api/v1/ic/products/:code`

```bash
curl http://localhost:8200/api/v1/ic/products/CON-01000 \
  -H "guid: smlx" \
  -H "databaseName: SML1_2026"
```

**Response:**
```json
{
  "success": true,
  "data": {
    "code": "CON-01000",
    "name": "ปูนซีเมนต์ 50 กก.",
    "name2": "",
    "unit_standard": "ถุง",
    "start_sale_unit": "ถุง",
    "start_sale_wh": "WH-01",
    "start_sale_shelf": "SH-01",
    "group_code": "CON",
    "balance_qty": 120.0,
    "price": 280.00
  }
}
```

### GET `/api/v1/ar/customers?page=1&size=10`

```bash
curl "http://localhost:8200/api/v1/ar/customers?page=1&size=10" \
  -H "guid: smlx" \
  -H "databaseName: SML1_2026"
```

**Response:**
```json
{
  "success": true,
  "data": [
    { "code": "AR00001", "name": "ลูกค้า จาก AI", "phone": "", "address": "" }
  ],
  "page": 1,
  "size": 10,
  "pages": 101
}
```

---

## Backward Compatibility

ไม่มี `/SMLJavaRESTService/...` แล้ว — ลบออกแล้วทั้งหมด  
ถ้ามี client เก่าที่ยังใช้ path เดิม ต้องอัปเดต path ให้ชี้มาที่ `/api/v1/...`

Auth headers เก่ายังใช้ได้:
- `guid:` → เทียบเท่า `X-Api-Key:`
- `databaseName:` → เทียบเท่า `X-Tenant:`

---

## Deployment

### Production (server 192.168.2.109)

```bash
# rsync code ขึ้นไป
rsync -av --exclude='.git' --exclude='.env' \
  /Users/your-local/sml-api-bybos/ \
  bosscatdog@192.168.2.109:~/sml-api-bybos/

# SSH เข้าไป rebuild
ssh bosscatdog@192.168.2.109
cd ~/sml-api-bybos
docker compose up -d --build
docker logs sml-api-bybos --tail=20
```

### Verify

```bash
curl http://192.168.2.109:8200/health
# {"status":"ok"}

curl http://192.168.2.109:8200/health/ready
# {"database":"ok","status":"ok"}
```

---

## Project Structure

```
sml-api-bybos/
├── cmd/server/main.go          ← entry point + route registration
├── internal/
│   ├── config/config.go        ← env loading
│   ├── db/pool.go              ← pgx connection pool per tenant
│   ├── handlers/
│   │   ├── health.go           ← /health + /health/ready
│   │   ├── product.go          ← GET /ic/products
│   │   ├── customer.go         ← GET /ar/customers
│   │   ├── supplier.go         ← GET /ap/suppliers
│   │   ├── transaction.go      ← GET/POST /ic/transactions
│   │   ├── stock.go            ← GET /ic/stock
│   │   ├── summary.go          ← GET /ic/transactions/summary
│   │   └── compat/
│   │       ├── read.go         ← GET /ic/warehouses (compat read)
│   │       └── write.go        ← POST sale-orders / sale-invoices / purchase-orders / products
│   └── middleware/
│       ├── auth.go             ← API key check
│       ├── tenant.go           ← database name selection + validation
│       ├── logger.go           ← zap structured logging
│       └── requestid.go        ← X-Request-ID header
├── Dockerfile
├── docker-compose.yml
└── .env.example
```

---

## License

Internal use — BossBoard / BillFlow projects
