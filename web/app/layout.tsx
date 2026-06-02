import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "FastAPI Stream Chat",
  description: "Minimal Next.js + AI SDK chat UI backed by FastAPI streaming",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
