import type { ReactNode } from "react";
import { Heading, Inline } from "@bzync/rui";
import { Icon, type IconName } from "../../shared/icons";

// Bordered panel used around catalog tables and action groups so every
// Operations view shares the same density and heading treatment.
export function Section({
  title,
  icon,
  actions,
  children,
}: {
  title: string;
  icon?: IconName;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="nsm-section">
      <header className="nsm-section-head">
        <Inline gap="xs" align="center" wrap={false}>
          {icon ? <Icon name={icon} className="nsm-section-icon" /> : null}
          <Heading as="h3" size="sm">{title}</Heading>
        </Inline>
        {actions ? <div className="nsm-section-actions">{actions}</div> : null}
      </header>
      <div className="nsm-section-body">{children}</div>
    </section>
  );
}

export function StatGrid({ children }: { children: ReactNode }) {
  return <div className="nsm-stat-grid">{children}</div>;
}
