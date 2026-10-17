#!/bin/bash
# Seed malxchange with demo data: 5 traders, balances, orders, trades
# Usage: ./scripts/seed-demo.sh [BASE_URL]
# Example: ./scripts/seed-demo.sh https://exchange.arifin.uk
#
# WARNING: For demo/development only. Uses direct DB inserts for funding.

set -e

BASE="${1:-http://localhost:8080}"
DB_CONTAINER="${DB_CONTAINER:-malxchange-postgres-1}"

echo "Seeding demo data on $BASE..."

# 1. Create 5 traders via API (requires manual email code step - use DB shortcut for demo)
echo "Creating traders..."
for i in 1 2 3 4 5; do
  EMAIL="trader$i@example.com"
  PASS="Trader${i}123!"
  # For demo: insert directly (bypasses email verification)
  docker exec "$DB_CONTAINER" psql -U malxchange -d malxchange -c "
    INSERT INTO api.members (id, email, password_hash, created_at)
    VALUES (gen_random_uuid(), '$EMAIL', crypt('$PASS', gen_salt('bf')), now())
    ON CONFLICT (email) DO NOTHING;" > /dev/null
  echo "  $EMAIL created"
done

# 2. Fund each trader with 10 BTC + 1,000,000 USDT
echo "Funding traders..."
docker exec "$DB_CONTAINER" psql -U malxchange -d malxchange -c "
  INSERT INTO wallet.ledger_accounts (member_id, coin, available)
  SELECT id, 'BTC', 10 FROM api.members WHERE email LIKE 'trader%@example.com'
  ON CONFLICT (member_id, coin) DO UPDATE SET available = 10;
  INSERT INTO wallet.ledger_accounts (member_id, coin, available)
  SELECT id, 'USDT', 1000000 FROM api.members WHERE email LIKE 'trader%@example.com'
  ON CONFLICT (member_id, coin) DO UPDATE SET available = 1000000;
  -- Journal entries for reconciliation
  INSERT INTO wallet.journal (id, member_id, coin, kind, avail_delta, amount, reason, idem_key)
  SELECT gen_random_uuid(), member_id, coin, 'adjustment', available, available, 'demo seed', 'seed-' || member_id || '-' || coin
  FROM wallet.ledger_accounts
  WHERE member_id IN (SELECT id FROM api.members WHERE email LIKE 'trader%@example.com')
  ON CONFLICT DO NOTHING;" > /dev/null
echo "  Each trader: 10 BTC + 1,000,000 USDT"

# 3. Add deposit records
echo "Adding deposit records..."
docker exec "$DB_CONTAINER" psql -U malxchange -d malxchange -c "
  INSERT INTO wallet.deposits (id, coin, member_id, address, txid, amount, detected_height, confirmations, status, credited_at)
  SELECT gen_random_uuid(), 'BTC', id, 'tb1qdemo' || substr(id::text, 1, 8),
         encode(gen_random_bytes(32), 'hex'), 10, 100, 6, 'credited', now()
  FROM api.members WHERE email LIKE 'trader%@example.com'
  ON CONFLICT DO NOTHING;" > /dev/null

# 4. Add withdraw addresses
echo "Adding withdraw addresses..."
docker exec "$DB_CONTAINER" psql -U malxchange -d malxchange -c "
  INSERT INTO api.withdraw_addresses (id, member_id, coin, address, label)
  SELECT gen_random_uuid()::text, id, 'BTC', 'tb1qwithdraw' || substr(id::text, 1, 8), 'My wallet'
  FROM api.members WHERE email LIKE 'trader%@example.com'
  ON CONFLICT DO NOTHING;" > /dev/null

# 5. Add favorites
echo "Adding favorites..."
docker exec "$DB_CONTAINER" psql -U malxchange -d malxchange -c "
  INSERT INTO api.market_favorites (member_id, market)
  SELECT id, 'BTC/USDT' FROM api.members WHERE email LIKE 'trader%@example.com'
  ON CONFLICT DO NOTHING;" > /dev/null

# 6. Place matching orders via API to generate trades
echo "Placing orders..."
login() {
  curl -s -X POST "$BASE/api/uc/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\": \"$1\", \"password\": \"$2\"}" | python3 -c "import json,sys; print(json.load(sys.stdin).get('token',''))"
}

place_order() {
  local token="$1" side="$2" price="$3" amount="$4"
  curl -s -X POST "$BASE/api/uc/orders" \
    -H "Content-Type: application/json" \
    -H "access-auth-token: $token" \
    -d "{\"market\": \"BTC/USDT\", \"side\": \"$side\", \"price\": \"$price\", \"amount\": \"$amount\", \"kind\": \"limit\"}" > /dev/null
}

T1=$(login "trader1@example.com" "Trader1123!")
T2=$(login "trader2@example.com" "Trader2123!")
T3=$(login "trader3@example.com" "Trader3123!")
T4=$(login "trader4@example.com" "Trader4123!")
T5=$(login "trader5@example.com" "Trader5123!")

place_order "$T1" "sell" "50000" "1.0"
place_order "$T2" "sell" "50100" "0.5"
place_order "$T3" "sell" "50200" "2.0"
place_order "$T4" "buy"  "50150" "1.5"
place_order "$T5" "buy"  "50050" "1.0"

echo ""
echo "Done! Demo accounts:"
for i in 1 2 3 4 5; do
  echo "  trader$i@example.com / Trader${i}123!"
done
echo ""
echo "Each has 10 BTC + 1,000,000 USDT, with orders and trade history."
