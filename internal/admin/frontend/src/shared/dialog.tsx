import { Button, ModalFooter } from "@bzync/rui";

// Read-only explorer dialogs share one footer: dismiss, then refresh.
// The refresh control keeps its width while the request is in flight.
export function ViewerDialogFooter({
  onClose,
  onRefresh,
  loading,
}: {
  onClose: () => void;
  onRefresh: () => void;
  loading: boolean;
}) {
  return (
    <ModalFooter>
      <Button variant="ghost" onClick={onClose}>Close</Button>
      <Button
        variant="primary"
        onClick={onRefresh}
        loading={loading}
        aria-label={loading ? "Refreshing" : "Refresh"}
      >
        Refresh
      </Button>
    </ModalFooter>
  );
}
