-- name: GetFormulaSource :one
SELECT body FROM economics.formula_sources
WHERE product_id=$1 AND period=$2 AND kind=$3 AND team_id=$4 AND portfolio=$5
ORDER BY version DESC LIMIT 1;

-- name: ListFormulaSources :many
SELECT body FROM economics.formula_sources
WHERE product_id=$1 AND period=$2 AND kind=$3 AND team_id=$4 AND portfolio=$5
ORDER BY version;

-- name: AppendFormulaSource :execrows
INSERT INTO economics.formula_sources(id,product_id,period,kind,team_id,portfolio,version,body,created_at)
SELECT sqlc.arg(id),sqlc.arg(product_id),sqlc.arg(period),sqlc.arg(kind),sqlc.arg(team_id),sqlc.arg(portfolio),sqlc.arg(version),sqlc.arg(body),sqlc.arg(created_at)
WHERE (SELECT COALESCE(MAX(version),0) FROM economics.formula_sources
 WHERE product_id=sqlc.arg(product_id) AND period=sqlc.arg(period) AND kind=sqlc.arg(kind) AND team_id=sqlc.arg(team_id) AND portfolio=sqlc.arg(portfolio))=sqlc.arg(expected_version)::integer;
