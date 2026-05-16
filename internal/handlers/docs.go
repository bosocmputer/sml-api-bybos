package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// swaggerUI is embedded HTML for Swagger UI (CDN-loaded, no bundled assets needed).
const swaggerUI = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>sml-api-bybos — API Docs</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
  <style>
    body { margin: 0; }
    #swagger-ui .topbar { background-color: #1a1a2e; }
    #swagger-ui .topbar .title { color: #fff; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: "/docs/openapi.json",
      dom_id: '#swagger-ui',
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      layout: "BaseLayout",
      deepLinking: true,
      defaultModelsExpandDepth: 1,
      defaultModelExpandDepth: 1,
    });
  </script>
</body>
</html>`

// openAPISpec is the embedded OpenAPI 3.0 specification.
const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {
    "title": "sml-api-bybos",
    "description": "REST API for SML ERP (PostgreSQL) — replaces SMLJavaRESTService3. All routes under /api/v1/ require API key + tenant headers.",
    "version": "1.0.0",
    "contact": { "name": "BossBoard / BillFlow" }
  },
  "servers": [
    { "url": "http://192.168.2.109:8200", "description": "Production" },
    { "url": "http://localhost:8200", "description": "Local dev" }
  ],
  "security": [{ "ApiKeyHeader": [], "TenantHeader": [] }],
  "components": {
    "securitySchemes": {
      "ApiKeyHeader": {
        "type": "apiKey", "in": "header", "name": "X-Api-Key",
        "description": "API key. Also accepted as 'guid' header (backward compat)."
      },
      "TenantHeader": {
        "type": "apiKey", "in": "header", "name": "X-Tenant",
        "description": "Database (tenant) name e.g. sml1_2026. Also accepted as 'databaseName' header."
      }
    },
    "schemas": {
      "SuccessResponse": {
        "type": "object",
        "properties": {
          "success": { "type": "boolean", "example": true },
          "data": {}
        }
      },
      "ErrorResponse": {
        "type": "object",
        "properties": {
          "success": { "type": "boolean", "example": false },
          "error": { "type": "string" }
        }
      },
      "PagedResponse": {
        "type": "object",
        "properties": {
          "success": { "type": "boolean" },
          "data": { "type": "array", "items": {} },
          "page": { "type": "integer" },
          "size": { "type": "integer" },
          "pages": { "type": "integer" }
        }
      },
      "Product": {
        "type": "object",
        "properties": {
          "code": { "type": "string", "example": "CON-01000" },
          "name": { "type": "string", "example": "ปูนซีเมนต์ 50 กก." },
          "name2": { "type": "string" },
          "unit_standard": { "type": "string", "example": "ถุง" },
          "start_sale_unit": { "type": "string", "example": "ถุง" },
          "start_sale_wh": { "type": "string", "example": "WH-01" },
          "start_sale_shelf": { "type": "string", "example": "SH-01" },
          "group_code": { "type": "string", "example": "CON" },
          "balance_qty": { "type": "number", "example": 120.0 },
          "price": { "type": "number", "example": 280.00 }
        }
      },
      "Customer": {
        "type": "object",
        "properties": {
          "code": { "type": "string", "example": "AR00001" },
          "name": { "type": "string", "example": "ลูกค้า จาก AI" },
          "phone": { "type": "string" },
          "address": { "type": "string" }
        }
      },
      "Supplier": {
        "type": "object",
        "properties": {
          "code": { "type": "string", "example": "V-001" },
          "name": { "type": "string" },
          "phone": { "type": "string" },
          "address": { "type": "string" }
        }
      },
      "Warehouse": {
        "type": "object",
        "properties": {
          "code": { "type": "string", "example": "WH-01" },
          "name": { "type": "string", "example": "คลังหลัก" },
          "shelves": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "code": { "type": "string", "example": "SH-01" },
                "name": { "type": "string" }
              }
            }
          }
        }
      },
      "Transaction": {
        "type": "object",
        "properties": {
          "doc_no": { "type": "string", "example": "BF-SO2604000001" },
          "doc_date": { "type": "string", "format": "date" },
          "trans_flag": { "type": "string", "example": "SO" },
          "cust_code": { "type": "string" },
          "total_amount": { "type": "number" },
          "items": { "type": "array", "items": { "$ref": "#/components/schemas/TransactionItem" } }
        }
      },
      "TransactionItem": {
        "type": "object",
        "properties": {
          "item_code": { "type": "string" },
          "item_name": { "type": "string" },
          "unit_code": { "type": "string" },
          "qty": { "type": "number" },
          "price": { "type": "number" },
          "sum_amount": { "type": "number" },
          "wh_code": { "type": "string" },
          "shelf_code": { "type": "string" }
        }
      },
      "StockItem": {
        "type": "object",
        "properties": {
          "item_code": { "type": "string" },
          "item_name": { "type": "string" },
          "wh_code": { "type": "string" },
          "shelf_code": { "type": "string" },
          "balance_qty": { "type": "number" },
          "unit_code": { "type": "string" }
        }
      },
      "DocItem": {
        "type": "object",
        "required": ["item_code", "unit_code", "qty", "price"],
        "properties": {
          "item_code": { "type": "string", "example": "CON-01000" },
          "item_name": { "type": "string" },
          "unit_code": { "type": "string", "example": "ถุง" },
          "wh_code": { "type": "string", "example": "WH-01" },
          "shelf_code": { "type": "string", "example": "SH-01" },
          "qty": { "type": "number", "example": 10 },
          "price": { "type": "number", "example": 250.00 },
          "sum_amount": { "type": "number", "example": 2500.00 }
        }
      },
      "SaleOrderRequest": {
        "type": "object",
        "required": ["doc_no", "doc_date", "cust_code", "items"],
        "properties": {
          "doc_no": { "type": "string", "example": "BF-SO2604000001" },
          "doc_date": { "type": "string", "format": "date", "example": "2026-04-28" },
          "doc_time": { "type": "string", "example": "09:00" },
          "cust_code": { "type": "string", "example": "AR00004" },
          "sale_code": { "type": "string" },
          "branch_code": { "type": "string", "default": "001" },
          "vat_type": { "type": "integer", "description": "0=แยกนอก, 1=รวมใน, 2=ศูนย์%", "default": 0 },
          "vat_rate": { "type": "number", "default": 7 },
          "remark": { "type": "string" },
          "items": { "type": "array", "items": { "$ref": "#/components/schemas/DocItem" } }
        }
      },
      "SaleInvoiceRequest": {
        "type": "object",
        "required": ["doc_no", "doc_date", "cust_code", "details"],
        "properties": {
          "doc_no": { "type": "string" },
          "doc_date": { "type": "string", "format": "date" },
          "doc_time": { "type": "string" },
          "cust_code": { "type": "string" },
          "sale_code": { "type": "string" },
          "vat_type": { "type": "integer", "default": 0 },
          "vat_rate": { "type": "number", "default": 7 },
          "remark": { "type": "string" },
          "details": { "type": "array", "items": { "$ref": "#/components/schemas/DocItem" }, "description": "ใช้ 'details' (ไม่ใช่ 'items') สำหรับ sale invoice" }
        }
      },
      "PurchaseOrderRequest": {
        "type": "object",
        "required": ["doc_no", "doc_date", "cust_code", "items"],
        "properties": {
          "doc_no": { "type": "string" },
          "doc_date": { "type": "string", "format": "date" },
          "doc_time": { "type": "string" },
          "cust_code": { "type": "string", "description": "Supplier code (ใช้ cust_code ตาม SML convention)" },
          "sale_code": { "type": "string" },
          "wh_code": { "type": "string", "description": "Default warehouse — overridden per item" },
          "shelf_code": { "type": "string" },
          "vat_type": { "type": "integer", "default": 0 },
          "vat_rate": { "type": "number", "default": 7 },
          "remark": { "type": "string" },
          "items": { "type": "array", "items": { "$ref": "#/components/schemas/DocItem" } }
        }
      },
      "CreateProductRequest": {
        "type": "object",
        "required": ["code", "name"],
        "properties": {
          "code": { "type": "string", "example": "TEST-001" },
          "name": { "type": "string", "example": "ทดสอบสินค้าใหม่" },
          "tax_type": { "type": "integer", "default": 0 },
          "item_type": { "type": "integer", "default": 0 },
          "unit_type": { "type": "integer", "default": 1 },
          "unit_cost": { "type": "string", "example": "ชิ้น" },
          "unit_standard": { "type": "string", "example": "ชิ้น" },
          "group_main": { "type": "string" },
          "purchase_point": { "type": "integer", "default": 0 },
          "units": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "unit_code": { "type": "string" },
                "unit_name": { "type": "string" },
                "stand_value": { "type": "number", "default": 1 },
                "divide_value": { "type": "number", "default": 1 }
              }
            }
          },
          "price_formulas": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "unit_code": { "type": "string" },
                "sale_type": { "type": "integer", "default": 0 },
                "price_0": { "type": "string", "example": "99.5" },
                "tax_type": { "type": "integer", "default": 0 },
                "price_currency": { "type": "integer", "default": 0 }
              }
            }
          }
        }
      }
    }
  },
  "paths": {
    "/health": {
      "get": {
        "tags": ["Health"],
        "summary": "Liveness check",
        "security": [],
        "responses": { "200": { "description": "ok", "content": { "application/json": { "schema": { "type": "object", "properties": { "status": { "type": "string", "example": "ok" } } } } } } }
      }
    },
    "/health/ready": {
      "get": {
        "tags": ["Health"],
        "summary": "Readiness check (tests DB connection)",
        "security": [],
        "responses": {
          "200": { "description": "ready", "content": { "application/json": { "schema": { "type": "object", "properties": { "status": { "type": "string" }, "database": { "type": "string", "example": "ok" } } } } } },
          "503": { "description": "not ready" }
        }
      }
    },
    "/api/v1/ic/products": {
      "get": {
        "tags": ["Products"],
        "summary": "List products (paginated)",
        "parameters": [
          { "name": "page", "in": "query", "schema": { "type": "integer", "default": 1 } },
          { "name": "size", "in": "query", "schema": { "type": "integer", "default": 100 } }
        ],
        "responses": {
          "200": { "description": "list of products", "content": { "application/json": { "schema": { "allOf": [ { "$ref": "#/components/schemas/PagedResponse" }, { "properties": { "data": { "type": "array", "items": { "$ref": "#/components/schemas/Product" } } } } ] } } } }
        }
      },
      "post": {
        "tags": ["Products"],
        "summary": "Create a new product in SML",
        "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/CreateProductRequest" } } } },
        "responses": {
          "200": { "description": "created", "content": { "application/json": { "schema": { "type": "object", "properties": { "success": { "type": "boolean" }, "data": { "type": "object", "properties": { "code": { "type": "string" } } } } } } } },
          "409": { "description": "product code already exists" }
        }
      }
    },
    "/api/v1/ic/products/{code}": {
      "get": {
        "tags": ["Products"],
        "summary": "Get product by item code",
        "parameters": [ { "name": "code", "in": "path", "required": true, "schema": { "type": "string" }, "example": "CON-01000" } ],
        "responses": {
          "200": { "description": "product found", "content": { "application/json": { "schema": { "allOf": [ { "$ref": "#/components/schemas/SuccessResponse" }, { "properties": { "data": { "$ref": "#/components/schemas/Product" } } } ] } } } },
          "404": { "description": "not found" }
        }
      }
    },
    "/api/v1/ic/sale-orders": {
      "post": {
        "tags": ["Documents"],
        "summary": "Create sale order (ใบสั่งขาย)",
        "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/SaleOrderRequest" } } } },
        "responses": {
          "200": { "description": "created", "content": { "application/json": { "schema": { "type": "object", "properties": { "success": { "type": "boolean" }, "data": { "type": "object", "properties": { "doc_no": { "type": "string" } } } } } } } },
          "400": { "description": "invalid payload" },
          "409": { "description": "doc_no already exists" }
        }
      }
    },
    "/api/v1/ic/sale-invoices": {
      "post": {
        "tags": ["Documents"],
        "summary": "Create sale invoice (ใบกำกับภาษี) — uses 'details' not 'items'",
        "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/SaleInvoiceRequest" } } } },
        "responses": {
          "200": { "description": "created" },
          "400": { "description": "invalid payload" },
          "409": { "description": "doc_no already exists" }
        }
      }
    },
    "/api/v1/ic/purchase-orders": {
      "post": {
        "tags": ["Documents"],
        "summary": "Create purchase order (ใบสั่งซื้อ)",
        "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/PurchaseOrderRequest" } } } },
        "responses": {
          "200": { "description": "created" },
          "400": { "description": "invalid payload" },
          "409": { "description": "doc_no already exists" }
        }
      }
    },
    "/api/v1/ic/transactions": {
      "get": {
        "tags": ["Transactions"],
        "summary": "List transactions (paginated + filterable)",
        "parameters": [
          { "name": "page", "in": "query", "schema": { "type": "integer", "default": 1 } },
          { "name": "size", "in": "query", "schema": { "type": "integer", "default": 100 } },
          { "name": "from", "in": "query", "schema": { "type": "string", "format": "date" }, "example": "2026-04-01" },
          { "name": "to", "in": "query", "schema": { "type": "string", "format": "date" }, "example": "2026-04-30" },
          { "name": "type", "in": "query", "schema": { "type": "string" }, "example": "SO", "description": "trans_flag filter: SO, SI, PO, ..." }
        ],
        "responses": {
          "200": { "description": "list of transactions", "content": { "application/json": { "schema": { "allOf": [ { "$ref": "#/components/schemas/PagedResponse" }, { "properties": { "data": { "type": "array", "items": { "$ref": "#/components/schemas/Transaction" } } } } ] } } } }
        }
      },
      "post": {
        "tags": ["Transactions"],
        "summary": "Create generic transaction",
        "requestBody": { "required": true, "content": { "application/json": { "schema": { "$ref": "#/components/schemas/SaleOrderRequest" } } } },
        "responses": { "200": { "description": "created" } }
      }
    },
    "/api/v1/ic/transactions/summary": {
      "get": {
        "tags": ["Transactions"],
        "summary": "Daily transaction summary (aggregate by trans_flag)",
        "parameters": [
          { "name": "from", "in": "query", "schema": { "type": "string", "format": "date" } },
          { "name": "to", "in": "query", "schema": { "type": "string", "format": "date" } }
        ],
        "responses": { "200": { "description": "daily summary" } }
      }
    },
    "/api/v1/ic/transactions/{doc_no}": {
      "get": {
        "tags": ["Transactions"],
        "summary": "Get transaction by doc_no",
        "parameters": [ { "name": "doc_no", "in": "path", "required": true, "schema": { "type": "string" } } ],
        "responses": {
          "200": { "description": "transaction found" },
          "404": { "description": "not found" }
        }
      }
    },
    "/api/v1/ic/stock": {
      "get": {
        "tags": ["Stock"],
        "summary": "List stock balances (paginated)",
        "parameters": [
          { "name": "page", "in": "query", "schema": { "type": "integer", "default": 1 } },
          { "name": "size", "in": "query", "schema": { "type": "integer", "default": 100 } }
        ],
        "responses": {
          "200": { "description": "stock list", "content": { "application/json": { "schema": { "allOf": [ { "$ref": "#/components/schemas/PagedResponse" }, { "properties": { "data": { "type": "array", "items": { "$ref": "#/components/schemas/StockItem" } } } } ] } } } }
        }
      }
    },
    "/api/v1/ic/stock/{code}": {
      "get": {
        "tags": ["Stock"],
        "summary": "Get stock balance by item code",
        "parameters": [ { "name": "code", "in": "path", "required": true, "schema": { "type": "string" } } ],
        "responses": {
          "200": { "description": "stock for item" },
          "404": { "description": "not found" }
        }
      }
    },
    "/api/v1/ic/warehouses": {
      "get": {
        "tags": ["Warehouses"],
        "summary": "List all warehouses",
        "responses": {
          "200": { "description": "warehouse list", "content": { "application/json": { "schema": { "allOf": [ { "$ref": "#/components/schemas/SuccessResponse" }, { "properties": { "data": { "type": "array", "items": { "$ref": "#/components/schemas/Warehouse" } } } } ] } } } }
        }
      }
    },
    "/api/v1/ic/warehouses/{code}": {
      "get": {
        "tags": ["Warehouses"],
        "summary": "Get warehouse with shelves by code",
        "parameters": [ { "name": "code", "in": "path", "required": true, "schema": { "type": "string" }, "example": "WH-01" } ],
        "responses": {
          "200": { "description": "warehouse with shelves" },
          "404": { "description": "not found" }
        }
      }
    },
    "/api/v1/ar/customers": {
      "get": {
        "tags": ["Customers"],
        "summary": "List customers (paginated)",
        "parameters": [
          { "name": "page", "in": "query", "schema": { "type": "integer", "default": 1 } },
          { "name": "size", "in": "query", "schema": { "type": "integer", "default": 100 } }
        ],
        "responses": {
          "200": { "description": "customer list", "content": { "application/json": { "schema": { "allOf": [ { "$ref": "#/components/schemas/PagedResponse" }, { "properties": { "data": { "type": "array", "items": { "$ref": "#/components/schemas/Customer" } } } } ] } } } }
        }
      }
    },
    "/api/v1/ar/customers/{code}": {
      "get": {
        "tags": ["Customers"],
        "summary": "Get customer by code",
        "parameters": [ { "name": "code", "in": "path", "required": true, "schema": { "type": "string" }, "example": "AR00001" } ],
        "responses": {
          "200": { "description": "customer found" },
          "404": { "description": "not found" }
        }
      }
    },
    "/api/v1/ap/suppliers": {
      "get": {
        "tags": ["Suppliers"],
        "summary": "List suppliers (paginated)",
        "parameters": [
          { "name": "page", "in": "query", "schema": { "type": "integer", "default": 1 } },
          { "name": "size", "in": "query", "schema": { "type": "integer", "default": 100 } }
        ],
        "responses": {
          "200": { "description": "supplier list", "content": { "application/json": { "schema": { "allOf": [ { "$ref": "#/components/schemas/PagedResponse" }, { "properties": { "data": { "type": "array", "items": { "$ref": "#/components/schemas/Supplier" } } } } ] } } } }
        }
      }
    },
    "/api/v1/ap/suppliers/{code}": {
      "get": {
        "tags": ["Suppliers"],
        "summary": "Get supplier by code",
        "parameters": [ { "name": "code", "in": "path", "required": true, "schema": { "type": "string" }, "example": "V-001" } ],
        "responses": {
          "200": { "description": "supplier found" },
          "404": { "description": "not found" }
        }
      }
    }
  },
  "tags": [
    { "name": "Health", "description": "Liveness and readiness checks" },
    { "name": "Products", "description": "Inventory products (ic_inventory)" },
    { "name": "Documents", "description": "Sale orders, sale invoices, purchase orders" },
    { "name": "Transactions", "description": "Transaction log (ic_trans)" },
    { "name": "Stock", "description": "Stock balance" },
    { "name": "Warehouses", "description": "Warehouses and shelves" },
    { "name": "Customers", "description": "AR customers (ar_customer)" },
    { "name": "Suppliers", "description": "AP suppliers (ap_supplier)" }
  ]
}`

// DocsHandler serves Swagger UI and the OpenAPI spec.
type DocsHandler struct{}

func NewDocsHandler() *DocsHandler { return &DocsHandler{} }

// UI serves the Swagger UI HTML page.
func (h *DocsHandler) UI(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, swaggerUI)
}

// Spec serves the OpenAPI JSON spec.
func (h *DocsHandler) Spec(c *gin.Context) {
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.String(http.StatusOK, openAPISpec)
}
