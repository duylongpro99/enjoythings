import type { Metadata } from "next";

import { FraudReviewConsole } from "@/components/fraud-review-console";

export const metadata: Metadata = {
  title: "Fraud Review Queue",
  description: "Operator queue for payments held in fraud review",
};

export default function FraudReviewsPage() {
  return (
    <main className="page-shell">
      <section className="chat-panel review-shell">
        <header className="chat-header">
          <h1>Fraud Review Queue</h1>
          <p>Payments held in FRAUD_REVIEW, the verdict that held them, and the audit trail behind each decision.</p>
        </header>
        <FraudReviewConsole />
      </section>
    </main>
  );
}
