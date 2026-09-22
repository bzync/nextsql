import { ToggleGroup, ToggleGroupItem } from "@bzync/rui";

export type SegmentOption<T extends string> = {
  value: T;
  label: string;
};

// Single-select choice group. ToggleGroup clears the value when the active
// item is clicked again; these choices always have one selection, so an
// empty change is ignored.
export function SegmentedControl<T extends string>({
  value,
  onChange,
  options,
  label,
  size = "sm",
}: {
  value: T;
  onChange: (value: T) => void;
  options: SegmentOption<T>[];
  label: string;
  size?: "sm" | "md" | "lg";
}) {
  return (
    <ToggleGroup
      type="single"
      size={size}
      variant="outline"
      value={value}
      aria-label={label}
      onValueChange={(next) => {
        if (next) onChange(next as T);
      }}
    >
      {options.map((option) => (
        <ToggleGroupItem key={option.value} value={option.value}>
          {option.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
