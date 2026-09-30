import type { ReactNode } from "react";
import "./globals.css";

export const metadata = {
  title: "Music",
  description: "Собственный музыкальный каталог",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="ru">
      <body>{children}</body>
    </html>
  );
}
