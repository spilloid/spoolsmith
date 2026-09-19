using System;
using System.IO;
using System.Linq;
using System.Threading;
using FlaUI.Core.AutomationElements;
using Xunit;

namespace SpoolSmithGui.Tests;

// Captures the real running app, never a mockup. Opt in on a Windows desktop.
public sealed class SiteScreenshots
{
    [StaFact]
    public void Capture_site_screenshots()
    {
        if (Environment.GetEnvironmentVariable("SPOOLSMITH_CAPTURE_SITE_SHOTS") != "1") return;
        using var fixture = new AppFixture();
        var images = Path.Combine(fixture.RepoRoot, "docs", "img");
        Directory.CreateDirectory(images);
        foreach (var (tab, file) in new[] {
            ("This PC", "gui-this-pc.png"),
            ("Add a printer", "gui-add-printer.png"),
            ("Review and apply", "gui-review-and-apply.png"),
            ("Inspect", "gui-tools.png") })
        {
            fixture.SelectTab(tab);
            Thread.Sleep(500);
            fixture.MainWindow.CaptureToFile(Path.Combine(images, file));
        }
        CaptureIntuneWizard(fixture, images);
    }

    /// <summary>
    /// The Intune wizard is a modal dialog opened from a button on the Tools
    /// tab, not a tab of its own, so the loop above never sees it. Captures
    /// both of its pages using the same profile/binary the wizard's own
    /// happy-path functional test drives, so the images shown on the product
    /// site match a package that actually validates, not a mockup.
    /// </summary>
    private static void CaptureIntuneWizard(AppFixture fixture, string images)
    {
        var profilePath = Path.Combine(fixture.RepoRoot, "examples", "intune", "accounting.json");
        var outputDir = Path.Combine(Path.GetTempPath(), "spoolsmith-intune-screenshot-" + Guid.NewGuid().ToString("N"));
        fixture.SelectTab("Tools");
        FunctionalTests.FindButton(fixture.MainWindow, "Build an Intune printer app...").Invoke();
        Window? dialog = null;
        FunctionalTests.WaitUntil(() => (dialog = fixture.MainWindow.ModalWindows
            .Concat(fixture.App.GetAllTopLevelWindows(fixture.Automation))
            .FirstOrDefault(w => w.Title == "Build an Intune printer app")) != null,
            "Intune wizard did not open.");
        try
        {
            FunctionalTests.FindVisibleIntuneControl(dialog!, "intune-profile");
            FunctionalTests.SetText(FunctionalTests.Find(dialog!, "intune-profile").AsTextBox(), profilePath);
            FunctionalTests.SetText(FunctionalTests.Find(dialog!, "intune-binary").AsTextBox(), fixture.CliExePath);
            // The bundled example profile carries no local driver archive.
            FunctionalTests.Find(dialog!, "Driver is managed separately and will be registered before installation").AsCheckBox().Click();
            FunctionalTests.WaitForText(FunctionalTests.Find(dialog!, "intune-display-name").AsTextBox(),
                t => t == "Example — Accounting Copier");
            FunctionalTests.SetText(FunctionalTests.Find(dialog!, "intune-output").AsTextBox(), outputDir);
            Thread.Sleep(300);
            dialog!.CaptureToFile(Path.Combine(images, "gui-intune-wizard.png"));

            FunctionalTests.FindButton(dialog!, "Validate and preview package").Invoke();
            FunctionalTests.WaitForText(FunctionalTests.FindVisibleIntuneControl(dialog!, "intune-preview").AsTextBox(),
                t => t.Contains("example-accounting-copier-", StringComparison.Ordinal));
            Thread.Sleep(300);
            dialog!.CaptureToFile(Path.Combine(images, "gui-intune-review.png"));
        }
        finally
        {
            try { if (Directory.Exists(outputDir)) Directory.Delete(outputDir, recursive: true); } catch { /* best-effort cleanup */ }
        }
    }
}
