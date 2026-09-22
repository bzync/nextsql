import { Button, Input } from "@bzync/rui";
import { Icon } from "./icons";

// One filter field for catalog and log tables. Operations views were each
// drawing a native search box; this is the same control, built from Input.
export function TableFilter({
  value,
  onChange,
  placeholder,
  label,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  label: string;
}) {
  return (
    <div className="nsm-table-search">
      <Input
        type="search"
        size="sm"
        value={value}
        placeholder={placeholder}
        aria-label={label}
        wrapperClassName="w-full"
        prefix={<Icon name="search" size={13} />}
        suffix={
          value ? (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-5"
              aria-label="Clear filter"
              onClick={() => onChange("")}
            >
              ×
            </Button>
          ) : null
        }
        onChange={(event) => onChange(event.currentTarget.value)}
      />
    </div>
  );
}
