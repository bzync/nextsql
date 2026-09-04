import { EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Text } from "@bzync/rui";
import type { ResultSet } from "./api";

export function ResultTable({ result, empty }: { result: ResultSet; empty?: string }) {
  if (!result || !result.columns || result.columns.length === 0) {
    return <EmptyState size="sm" title={empty ?? "No data"} />;
  }
  if (!result.rows || result.rows.length === 0) {
    return <EmptyState size="sm" title={empty ?? "No rows"} />;
  }
  return (
    <Table density="compact">
      <TableHeader>
        <TableRow>
          {result.columns.map((c) => (
            <TableHead key={c}>{c}</TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {result.rows.map((row, i) => (
          <TableRow key={i}>
            {row.map((cell, j) => (
              <TableCell key={j}>
                {cell === null ? (
                  <Text as="span" variant="muted">
                    NULL
                  </Text>
                ) : (
                  cell
                )}
              </TableCell>
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
