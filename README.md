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
| `SERVER_PORT` | `8200` | TCP port |
| `SERVER_HOST` | `0.0.0.0` | Bind address |
| `API_KEYS` | `dev-key` | Comma-separated valid API keys เช่น `smlx,dev-key` |
| `SML_DB_HOST` | `192.168.2.248` | Default SML PostgreSQL host |
| `SML_DB_PORT` | `5432` | Default PostgreSQL port |
| `SML_DB_USER` | `postgres` | Default database user |
| `SML_DB_PASSWORD` | `sml` | Default database password |
| `SML_DB_SSLMODE` | `disable` | PostgreSQL sslmode |
| `SML_DB_MAX_CONNS` | `10` | Max connections per tenant pool |
| `SML_DB_MIN_CONNS` | `2` | Min connections per tenant pool |
| `DEFAULT_TENANT` | *(empty)* | Tenant ที่ใช้ถ้าไม่ระบุ header |
| `ALLOWED_TENANTS` | *(empty = allow all)* | Comma-separated tenant ที่อนุญาต เช่น `sml1_2026,aoy,data1_test` |
| `SML_AUTH_MAIN_DATABASE` | `smlerpmaindata` | Database หลักสำหรับ `POST /api/v1/auth/sml/login` (ตรวจ user/password และสิทธิ์ database) |
| `SML_AUTH_PROVIDER` | `data` | ค่า `provider` ที่ยอมรับใน login request |
| `SML_AUTH_DATAGROUP` | `sml` | ค่า `dataGroup` ที่ยอมรับใน login request |
| `SML_IMAGE_TEMPLATE_DATABASE` | `iampcoffee_images` | Template database default สำหรับ tenant readiness/provisioning ของ `${tenant}_images` |

Production ต้องกำหนด `SML_IMAGE_TEMPLATE_DATABASE` ให้เป็น `_images` database มาตรฐานของลูกค้ารายนั้นโดยตรง เช่น `vrh_images` สำหรับวิรัชโฮมมาร์ท ค่า fallback ใช้สำหรับ development เท่านั้น

> A tenant can override the PostgreSQL connection without moving the other tenants:
>
> ```env
> SML_DB_HOST_AOY=demserver.3bbddns.com
> SML_DB_PORT_AOY=47309
> SML_DB_USER_AOY=postgres
> SML_DB_PASSWORD_AOY=...
> SML_DB_SSLMODE_AOY=disable
> ALLOWED_TENANTS=sml1_2026,aoy,data1_test
> ```

---

## Authentication

ทุก endpoint ภายใต้ `/api/v1/` ต้องส่ง API key ผ่าน header ใดหนึ่ง:

```
X-Api-Key: smlx
# หรือ (backward compat)
guid: smlx
# หรือสำหรับ curl/testing
?api_key=smlx
```

PaperLess user synchronization uses two internal auth endpoints that run before tenant-header middleware but remain protected by the API key:

- `POST /api/v1/auth/sml/users/sync-candidates` returns active users plus the built-in SML `superadmin` account, along with saved-signature availability, fingerprint, dimensions, and decoded byte size. Some SML installations keep this login-capable administrator at `active_status=0`; other inactive users remain excluded. The endpoint never returns Base64 image data.
- `POST /api/v1/auth/sml/users/signature` returns the JPEG/PNG bytes for one allowed sync candidate after validating `databaseName`, `userCode`, and `expectedVersion`.

Saved signatures are matched with `TRIM(UPPER(erp_user.code)) = TRIM(UPPER(sml_user_list.user_code))`. Both raw Base64 and PNG/JPEG data URLs are accepted. Request/image bytes, Base64, passwords, and signature fingerprints must not be written to logs.

---

## Multi-Tenant

ระบุ database (tenant) ผ่าน header ใดหนึ่ง:

```
X-Tenant: sml1_2026
# หรือ (backward compat)
databaseName: SML1_2026
```

> Header value ไม่ case-sensitive — `SML1_2026` และ `sml1_2026` ใช้ได้เหมือนกัน  
> ถ้าไม่ส่ง header จะใช้ `DEFAULT_TENANT`

**Tenants ที่รองรับ** ขึ้นกับ `ALLOWED_TENANTS` ของ instance นั้น ๆ  
ตัวอย่างที่ใช้งานกับ BillFlow ตอนนี้: `sml1_2026`, `aoy`, `data1_test`

### Tenant Image DB Preflight

PaperLess document snapshots require both databases to exist:

- Main tenant DB: `${tenant}`
- Image DB: `${tenant}_images`

Both databases must contain `public.sml_doc_images` with the same schema. Run this before enabling a tenant or before customer smoke tests:

```bash
go run ./cmd/verify-sml-tenant --tenant stpt --template iampcoffee_images
go run ./cmd/verify-sml-tenant --all-allowed --template iampcoffee_images
```

In Docker deployments the same binaries are copied into the runtime image:

```bash
docker exec paperless-prod-sml-api ./verify-sml-tenant --tenant stpt --template iampcoffee_images
```

The runtime API also exposes a readiness endpoint for PaperLess login checks:

```bash
curl -H "X-Api-Key: $API_KEY" "http://localhost:8200/api/v1/tenants/readiness?tenant=stpt"
```

PaperLess can also trigger a guarded runtime provision when the main tenant DB exists but `${tenant}_images` or `public.sml_doc_images` is missing:

```bash
curl -X POST \
  -H "X-Api-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"tenant":"stpt"}' \
  "http://localhost:8200/api/v1/tenants/image-database"
```

The endpoint creates only the missing `${tenant}_images` database and/or missing `public.sml_doc_images` table, then verifies it against the configured template. It refuses main DB missing and existing schema mismatch cases.

If the main DB exists but the image DB is missing because the tenant was created directly in PostgreSQL, provision it explicitly. The command is dry-run by default:

```bash
go run ./cmd/provision-sml-image-db --tenant stpt --template iampcoffee_images
go run ./cmd/provision-sml-image-db --tenant stpt --template iampcoffee_images --apply
go run ./cmd/verify-sml-tenant --tenant stpt --template iampcoffee_images
```

The provision command creates only `${tenant}_images`, copies no image data from the template, and builds `public.sml_doc_images` from a real `_images` schema. If a just-created image DB is wrong and still empty, drop only that `${tenant}_images` database after customer approval.

---

## Swagger UI

Swagger UI ใช้ official `swagger-ui-dist` assets ที่ฝังอยู่ใน binary แล้ว ไม่ต้องพึ่ง CDN ตอนเปิดบน LAN/server

เปิด browser ไปที่:

```
http://localhost:8200/docs
```

หรือดู OpenAPI spec โดยตรง:

```
http://localhost:8200/docs/openapi.json
http://localhost:8200/openapi.json
```

---

## Endpoints

### Auth

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/auth/sml/login` | ตรวจ user/password กับฐาน `SML_AUTH_MAIN_DATABASE` และคืนรายการ database ที่ user คนนั้นมีสิทธิ์ (ใช้ registry DB ไม่ผ่าน tenant header) |

### Health

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Liveness check |
| `GET` | `/health/ready` | Readiness check (ตรวจ DB connection) |

### Tenants (PaperLess image DB)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/tenants/readiness?tenant=` | ตรวจว่า tenant DB และ `${tenant}_images` พร้อมใช้งาน (schema `public.sml_doc_images` ตรงกับ template) |
| `POST` | `/api/v1/tenants/image-database` | สร้าง/ซ่อม `${tenant}_images` และ schema `sml_doc_images` แบบ guarded ถ้ายังไม่พร้อม |

### Marketplace

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/marketplace/nextstep/orders?date_from=&date_to=` | อ่าน NextStep Marketplace จากเอกสาร `ic_qt` ที่เลขเอกสารขึ้นต้น `MQT` หรือ `PREQT`; `cust_code` ไม่ใช้เป็น filter แล้ว |

### Inventory / Products (`ic`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ic/products` | รายการสินค้าทั้งหมด (paginated) |
| `GET` | `/api/v1/ic/products/:code` | สินค้าตาม item code |
| `GET` | `/api/v1/ic/products/:code/units` | หน่วยที่ใช้ได้ของสินค้านั้น |
| `GET` | `/api/v1/ic/products/:code/images` | รายการ metadata รูปสินค้า จากฐาน `${tenant}_images` |
| `GET` | `/api/v1/ic/products/:code/images/:roworder` | ไฟล์รูปสินค้า binary |
| `GET` | `/api/v1/ic/units` | รายการหน่วยสินค้า active |
| `POST` | `/api/v1/ic/products` | สร้างสินค้าใหม่ใน SML |

### Sale Orders / Invoices / Purchase Orders

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/ic/sale-orders` | สร้างใบสั่งขาย (sale order) |
| `POST` | `/api/v1/ic/sale-invoices` | สร้างใบกำกับภาษี (sale invoice) |
| `POST` | `/api/v1/ic/sale-invoices/:doc_no/cancel/preview` | Preview ใบลดหนี้ (credit note) ที่จะสร้างจากใบกำกับภาษีเดิม โดยไม่เขียนข้อมูล |
| `POST` | `/api/v1/ic/sale-invoices/:doc_no/cancel` | สร้างใบลดหนี้ยกเลิกใบกำกับภาษี (`trans_flag` credit note); idempotent ถ้ามีใบลดหนี้อยู่แล้วจะคืน `status=already_exists` |
| `POST` | `/api/v1/ic/purchase-orders` | สร้างใบสั่งซื้อ (purchase order) |
| `PATCH` | `/api/v1/ic/purchase-orders/:doc_no/creditor` | แก้เจ้าหนี้ของใบสั่งซื้อเดิม โดยอัปเดต `ic_trans.cust_code` และ `ic_trans_detail.cust_code` |
| `PATCH` | `/api/v1/ic/purchase-orders/:doc_no/doc-ref` | แก้ `doc_ref` ของใบสั่งซื้อเดิม (header + detail); รองรับ `dry_run` และ optimistic-lock ผ่าน `expected_old_doc_ref`/`expected_remark_5` |

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

### Document Formats / Document Numbers / Document Search

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ic/doc-formats` | รูปแบบเอกสารทั้งหมดจาก `erp_doc_format`; ส่ง `screen_code` ได้ถ้าต้องการ filter |
| `GET` | `/api/v1/ic/doc-formats/by-code?doc_format_code=PO` | ค้นหารูปแบบเอกสารด้วย `erp_doc_format.code` และคืน `screen_code` ของรายการนั้น |
| `GET` | `/api/v1/ic/doc-no/next` | ดูเลขเอกสารถัดไปจาก SML สำหรับ `saleorder`, `saleinvoice`, `purchaseorder`, `receipt` |
| `GET` | `/api/v1/ic/document-candidates?doc_format_code=` | ค้นหาเอกสารจาก `ic_trans UNION ALL ap_ar_trans` ตาม `doc_format_code`; `search` เป็น contains literal บน `doc_no`, `cust_code`, `ar_customer.name_1`, `ap_supplier.name_1` |
| `POST` | `/api/v1/ic/document-candidates/batch` | ตรวจเลขเอกสารแบบ exact match สูงสุด 30 รายการจากทั้ง `ic_trans` และ `ap_ar_trans`; ใช้สำหรับ PaperLess batch import |
| `GET` | `/api/v1/ic/document-candidates/:doc_no?doc_format_code=` | เอกสารเดี่ยวจากตารางเดียวกับ document-candidates |

### PaperLess Document Finalization

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/documents/:doc_no/images` | Replace รูป snapshot เอกสารใน `public.sml_doc_images` ทั้ง tenant DB และ `${tenant}_images` สูงสุด 8 JPEG pages |
| `POST` | `/api/v1/documents/:doc_no/lock` | Lock เอกสาร SML หลัง PaperLess ส่งรูปและ final evidence สำเร็จ |
| `GET` | `/api/v1/documents/:doc_no/related?doc_format_code=&depth=` | กราฟเอกสารอ้างอิงสำหรับ PaperLess; `depth` default 3, สูงสุด 4, คืนสูงสุด 30 nodes |

`/documents/:doc_no/images` ต้องส่ง `images[]` ที่มี `pageNo`, `contentType=image/jpeg`, `sha256`, และ `data` base64 (สูงสุด 8 pages, 4MB ต่อ page). Endpoint นี้ replace rows เดิมของ `doc_no` เพื่อให้ retry ไม่สร้างรูปซ้ำ.

`/documents/:doc_no/lock` ตั้ง `is_lock_record=1` บน `ic_trans` หรือ `ap_ar_trans` (ค้นหาให้อัตโนมัติ); idempotent — lock ซ้ำคืน `already_locked=true` โดยไม่เขียนซ้ำ.

For large customer tenants, document-candidate search may need DBA-approved indexes such as trigram indexes on `doc_no`, `cust_code`, and party names. The API does not create customer ERP indexes automatically.

### Accounts Receivable / Customers (`ar`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ar/customers` | รายการลูกค้า (paginated) |
| `GET` | `/api/v1/ar/customers/:code` | ลูกค้าตาม customer code |
| `POST` | `/api/v1/ar/customers` | สร้างลูกหนี้ใหม่ โดยเขียน `ar_customer` + `ar_customer_detail` แบบ transaction |
| `POST` | `/api/v1/ar/receipt-candidates` | ตรวจ Shopee order ว่ามีใบขาย SML และเคยรับชำระแล้วหรือยัง |
| `POST` | `/api/v1/ar/receipts` | สร้างเอกสารรับชำระ SML (`ap_ar_trans`, `ap_ar_trans_detail`, `cb_trans`, `cb_trans_detail`) |

### Accounts Payable / Suppliers (`ap`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/ap/suppliers` | รายการ supplier (paginated) |
| `GET` | `/api/v1/ap/suppliers/:code` | Supplier ตาม supplier code |
| `POST` | `/api/v1/ap/suppliers` | สร้างเจ้าหนี้ใหม่ โดยเขียน `ap_supplier` + `ap_supplier_detail` แบบ transaction |

### ERP Master Pickers

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/erp/branches` | สาขาจาก `erp_branch_list` |
| `GET` | `/api/v1/erp/users` | ผู้ใช้/พนักงานขายจาก `erp_user` |
| `GET` | `/api/v1/erp/expenses` | ค่าใช้จ่ายจาก `erp_expenses_list` |
| `GET` | `/api/v1/erp/incomes` | รายได้จาก `erp_income_list` |
| `GET` | `/api/v1/erp/passbooks` | สมุดบัญชี/บัญชีรับเงินจาก `erp_pass_book` |
| `GET` | `/api/v1/erp/sml-user-list` | ผู้ใช้ SML จากฐาน `smlerpmaindata.sml_user_list` (fixed database, ไม่ผ่าน tenant header) |

---

## Query Parameters

### Pagination (GET list endpoints)

| Parameter | Default | Description |
|---|---|---|
| `page` | `1` | หน้าที่ต้องการ |
| `size` | endpoint-specific | จำนวนรายการต่อหน้า; master/picker ส่วนใหญ่ cap ที่ `200` |
| `search` | *(empty)* | ค้นหาจาก `code`, `name_1` และ field เพิ่มเติมที่ endpoint รองรับ |

### Transaction Filters (`GET /api/v1/ic/transactions`)

| Parameter | Description |
|---|---|
| `from` | วันเริ่มต้น (YYYY-MM-DD) |
| `to` | วันสิ้นสุด (YYYY-MM-DD) |
| `type` | trans_flag เช่น `SO`, `SI`, `PO` |

### Document Number Preview (`GET /api/v1/ic/doc-no/next`)

| Parameter | Description |
|---|---|
| `route` | `saleorder`, `saleinvoice`, `purchaseorder`, `receipt` หรือ alias `so`, `si`, `po`, `rc` |
| `prefix` | prefix เลขเอกสาร เช่น `RC`, `PO` |
| `format` | format เลขรัน เช่น `YYMM####`, `@YYMM####` |
| `doc_date` | วันที่เอกสาร `YYYY-MM-DD`; ถ้าไม่ส่งใช้วันที่ server |

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
  "data": {
    "doc_no": "BF-SO2604000001",
    "status": "created",
    "rows_written": 1,
    "log_status": "created"
  }
}
```

> หลังสร้าง sale order / sale invoice / purchase order สำเร็จ ระบบจะพยายามเขียน
> `erp_logs` ในฐาน `${tenant}_logs` เพิ่มเติม ถ้า logs DB ไม่มีหรือเชื่อมต่อไม่ได้
> เอกสารหลักยังสำเร็จ แต่ response จะมี `log_status="warning"` และ `log_warning`
> เป็นข้อความปลอดภัยสำหรับนำไปแสดงใน BillFlow.

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
  "meta": {
    "total": 1001,
    "page": 1,
    "size": 10
  }
}
```

### GET `/api/v1/ic/doc-formats?screen_code=EE`

```bash
curl "http://localhost:8200/api/v1/ic/doc-formats?screen_code=EE" \
  -H "X-Api-Key: smlx" \
  -H "X-Tenant: aoy"
```

**Response:**
```json
{
  "success": true,
  "data": [
    {
      "code": "RC",
      "name_1": "รับชำระหนี้",
      "name_2": "",
      "format": "@YYMM####",
      "screen_code": "EE"
    }
  ]
}
```

### GET `/api/v1/ic/doc-formats/by-code?doc_format_code=RC`

```bash
curl "http://localhost:8200/api/v1/ic/doc-formats/by-code?doc_format_code=RC" \
  -H "X-Api-Key: smlx" \
  -H "X-Tenant: aoy"
```

**Response:**
```json
{
  "success": true,
  "data": {
    "code": "RC",
    "name_1": "รับชำระหนี้",
    "name_2": "",
    "format": "@YYMM####",
    "screen_code": "EE"
  }
}
```

### POST `/api/v1/ar/receipt-candidates`

ใช้ตรวจว่า Shopee order ใด match กับใบขาย SML แล้ว และเคยถูกนำไปรับชำระหรือยัง
ก่อนสร้างเอกสาร `RC`.

```bash
curl -X POST http://localhost:8200/api/v1/ar/receipt-candidates \
  -H "X-Api-Key: smlx" \
  -H "X-Tenant: aoy" \
  -H "Content-Type: application/json" \
  -d '{
    "order_sns": ["2605090756X9R7", "2605114N3PXCA5"]
  }'
```

**Response:**
```json
{
  "success": true,
  "data": {
    "items": [
      {
        "order_sn": "2605090756X9R7",
        "invoice_doc_no": "BF-INV26050001",
        "invoice_doc_date": "2026-05-09",
        "cust_code": "AR00001",
        "invoice_amount": 1000,
        "already_received": false,
        "status": "ready"
      }
    ]
  }
}
```

### POST `/api/v1/ar/receipts`

สร้างรับชำระ SML สำหรับ Shopee settlement โดยเขียน `ap_ar_trans`,
`ap_ar_trans_detail`, `cb_trans`, และ `cb_trans_detail` ด้วย `trans_flag=239`.
ถ้า `payout_amount` น้อยกว่ายอดใบขาย ต้องส่ง `expense_code` เพื่อบันทึกส่วนต่างเป็นค่าใช้จ่าย
(`cb_trans_detail.doc_type=11`).

```bash
curl -X POST http://localhost:8200/api/v1/ar/receipts \
  -H "X-Api-Key: smlx" \
  -H "X-Tenant: aoy" \
  -H "Content-Type: application/json" \
  -d '{
    "doc_date": "2026-05-27",
    "doc_time": "14:30",
    "doc_format_code": "RC",
    "remark": "รับชำระ Shopee จาก BillFlow",
    "passbook_code": "BANK001",
    "expense_code": "5015",
    "lines": [
      {
        "order_sn": "2605090756X9R7",
        "invoice_doc_no": "BF-INV26050001",
        "payout_amount": 940.50
      }
    ]
  }'
```

**Response:**
```json
{
  "success": true,
  "data": {
    "doc_no": "RC26050011",
    "status": "created",
    "invoice_count": 1,
    "invoice_amount": 1000,
    "payout_amount": 940.5,
    "difference_amount": 59.5,
    "passbook_code": "BANK001",
    "expense_code": "5015",
    "trans_flag": 239,
    "trans_type": 2
  }
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
│   │   ├── docs.go             ← /docs + /openapi.json
│   │   ├── auth.go             ← POST /auth/sml/login
│   │   ├── tenant_readiness.go ← GET /tenants/readiness, POST /tenants/image-database
│   │   ├── product.go          ← products, units, product images
│   │   ├── customer.go         ← AR customer list/get/create
│   │   ├── supplier.go         ← AP supplier list/get/create
│   │   ├── erp_master.go       ← branches/users/expenses/incomes/passbooks/sml-user-list
│   │   ├── doc_format.go       ← erp_doc_format list
│   │   ├── doc_no.go           ← next doc number preview
│   │   ├── document_candidate.go ← GET/POST /ic/document-candidates (single/search/batch)
│   │   ├── document_image.go   ← POST /documents/:doc_no/images
│   │   ├── lock.go             ← POST /documents/:doc_no/lock
│   │   ├── related_document.go ← GET /documents/:doc_no/related
│   │   ├── ar_receipt.go       ← Shopee settlement receipt writer
│   │   ├── transaction.go      ← GET/POST /ic/transactions
│   │   ├── stock.go            ← GET /ic/stock
│   │   ├── summary.go          ← GET /ic/transactions/summary
│   │   └── compat/
│   │       ├── read.go         ← GET /ic/warehouses (compat read)
│   │       ├── write.go        ← POST sale-orders / sale-invoices / purchase-orders / products, PATCH creditor/doc-ref
│   │       └── cancel_invoice.go ← POST /ic/sale-invoices/:doc_no/cancel(/preview)
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
